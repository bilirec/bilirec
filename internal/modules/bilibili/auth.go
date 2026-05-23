package bilibili

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	bili "github.com/CuteReimu/bilibili/v2"
	"github.com/go-resty/resty/v2"
)

var (
	ErrNotControllerMode     = errors.New("后端没有启用 controller 模式")
	ErrQRCodeLoginInProgress = errors.New("已有进行中的二维码登录")
)

// loadCookiesOrLogin run on startup mode
func (c *Client) loadCookiesOrLogin() error {
	defer c.syncCookies()

	if cookie, token, err := c.loadOfflineCredentials(); err == nil {
		c.SetCookiesString(cookie)
		c.wbi.WithCookies(c.GetCookies())
		c.refreshToken = token

		if acc, err := c.GetAccountInformation(); err == nil {
			logger.Infof("已加载用户 Cookie：%s（mid：%d）", acc.Uname, acc.Mid)
			c.updateSession(func(as *AuthSession) {
				as.State = StatePreloaded
				as.Account = acc
				as.Error = nil
			})
			if err := c.refreshCookiesIfRequired(); err != nil {
				logger.Warnf("预加载刷新检查失败：%v", err)
			}
			go c.refreshCookiesPeriodically(c.ctx, 10*time.Minute)
			return nil
		} else {
			logger.Warnf("使用已加载 Cookie 获取账号信息失败：%v", err)
		}

	} else if !os.IsNotExist(err) {
		return fmt.Errorf("读取 cookie 文件失败：%v", err)
	}

	logger.Info("开始哔哩哔哩登录流程")

	qrcode, err := c.GetQRCode()
	if err != nil {
		return fmt.Errorf("获取二维码失败：%v", err)
	}

	logger.Info("请扫描二维码登录：")
	qrcode.Print()

	// blocking thread
	result, err := c.LoginWithQRCode(bili.LoginWithQRCodeParam{
		QrcodeKey: qrcode.QrcodeKey,
	})

	if err != nil {
		return fmt.Errorf("二维码登录失败：%v", err)
	} else if result.Code != 0 {
		return fmt.Errorf("登录失败：%s（代码 %d）", result.Message, result.Code)
	}

	if acc, err := c.GetAccountInformation(); err != nil {
		return fmt.Errorf("登录后获取账号信息失败：%v，请重试。", err)
	} else {
		logger.Infof("登录成功，当前账号：%s（mid：%d）", acc.Uname, acc.Mid)
		c.updateSession(func(as *AuthSession) {
			as.State = StateAuthenticated
			as.Account = acc
			as.Error = nil
		})
	}

	if err := c.writeRefreshTokenToFile(result.RefreshToken); err != nil {
		logger.Warn(err)
	}
	if err := c.writerCookiesToFile(); err != nil {
		logger.Warn(err)
	}

	go c.refreshCookiesPeriodically(c.ctx, 10*time.Minute)
	return nil
}

// preloadCookies run on controller mode
func (c *Client) preloadCookies() error {
	if c.loginMode != "controller" {
		logger.Warnf("在 %s 模式下调用 preloadCookies，已跳过", c.loginMode)
		return nil
	}

	cookie, token, err := c.loadOfflineCredentials()
	if err != nil {
		// If no offline credentials available, that's OK in controller mode
		// QR login will be triggered via controller endpoint
		logger.Debugf("未找到离线凭据：%v", err)
		return nil
	}

	defer c.syncCookies()

	c.SetCookiesString(cookie)
	c.wbi.WithCookies(c.GetCookies())
	c.refreshToken = token

	if acc, err := c.GetAccountInformation(); err == nil {
		logger.Infof("已加载用户 Cookie：%s（mid：%d）", acc.Uname, acc.Mid)
		refreshCtx, cancel := context.WithCancel(c.ctx)
		c.updateSession(func(s *AuthSession) {
			s.State = StatePreloaded
			s.Account = acc
			s.Error = nil
			s.cookieRefreshCancel = cancel
		})
		// Best effort refresh, don't fail if it doesn't work
		if err := c.refreshCookiesIfRequired(); err != nil {
			logger.Debugf("预加载刷新检查失败（非阻塞）：%v", err)
		}
		go c.refreshCookiesPeriodically(refreshCtx, 10*time.Minute)
		return nil
	}

	// If validation fails, log but don't fail startup
	logger.Warnf("使用预加载 Cookie 获取账号信息失败：%v", err)
	return nil
}

func (c *Client) refreshCookiesIfRequired() error {
	_, err, _ := c.refreshSF.Do("bilibili_refresh_cookies", func() (any, error) {
		info, err := c.GetWebCookieRefreshInfo()
		if err != nil {
			return nil, fmt.Errorf("获取 cookie 刷新信息失败：%v", err)
		}
		if !info.Refresh {
			logger.Info("Cookie 无需刷新")
			return nil, nil
		}
		csrfResult, err := c.GetWebCookieRefreshCsrf(bili.GetWebCookieRefreshCsrfParam{
			Timestamp: info.Timestamp,
		})
		if err != nil {
			return nil, fmt.Errorf("获取 cookie 刷新 csrf 失败：%v", err)
		}
		refreshed, err := c.RefreshCookie(bili.RefreshCookieParam{
			RefreshToken: c.refreshToken,
			RefreshCsrf:  csrfResult.RefreshCsrf,
		})
		if err != nil {
			return nil, fmt.Errorf("刷新 cookie 失败：%v", err)
		} else {
			logger.Info("Cookie 刷新成功")
			c.syncCookies()
		}

		if err := c.writeRefreshTokenToFile(refreshed.RefreshToken); err != nil {
			return nil, err
		}
		return nil, c.writerCookiesToFile()
	})
	return err
}

func (c *Client) writerCookiesToFile() error {
	cookieStr := c.GetCookiesString()
	if err := os.WriteFile(c.cookiePath, []byte(cookieStr), 0600); err != nil {
		return fmt.Errorf("写入 cookie 文件失败：%v", err)
	}
	return nil
}

func (c *Client) writeRefreshTokenToFile(refreshToken string) error {
	c.refreshToken = refreshToken
	if err := os.WriteFile(c.refreshTokenPath, []byte(refreshToken), 0600); err != nil {
		return fmt.Errorf("写入 refresh token 文件失败：%v", err)
	}
	return nil
}

func (c *Client) loadOfflineCredentials() (cookie string, refreshToken string, err error) {

	cookieBytes, err := os.ReadFile(c.cookiePath)
	if os.IsNotExist(err) {
		cookie = ""
	} else if err != nil {
		err = fmt.Errorf("读取 cookie 文件失败：%v", err)
	} else {
		cookie = string(cookieBytes)
	}

	refreshTokenBytes, err := os.ReadFile(c.refreshTokenPath)
	if os.IsNotExist(err) {
		refreshToken = ""
	} else if err != nil {
		err = fmt.Errorf("读取 refresh token 文件失败：%v", err)
	} else {
		refreshToken = string(refreshTokenBytes)
	}

	return
}

func (c *Client) refreshCookiesPeriodically(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := c.refreshCookiesIfRequired(); err != nil {
				logger.Error(err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (c *Client) syncCookies() {
	mainCookies := c.GetCookies()
	// 更新或添加 cookies 到 liveClient
	go syncCookieToClient(c.liveClient, mainCookies)
	// 同样处理 liveStreamClient
	go syncCookieToClient(c.liveStreamClient, mainCookies)
}

func syncCookieToClient(client *resty.Client, cookies []*http.Cookie) {
	for _, mainCookie := range cookies {
		found := false
		for j, streamCookie := range client.Cookies {
			if mainCookie.Name == streamCookie.Name {
				client.Cookies[j] = mainCookie
				found = true
				break
			}
		}
		if !found {
			client.Cookies = append(client.Cookies, mainCookie)
		}
	}
}

// InitQRLogin initiates a new QR login session for controller mode (non-blocking)
// Returns a QR code payload. When reusing an existing pending session, only Url is populated.
// If already authenticated, starts a new login session (allowing account switching).
func (c *Client) InitQRLogin() (*bili.QRCode, error) {
	if c.loginMode != "controller" {
		logger.Warnf("在 %s 模式下调用 InitQRLogin，已跳过", c.loginMode)
		return nil, ErrNotControllerMode
	}

	session := c.GetSession()
	// Allow reusing existing QR code if we're still in the awaiting/authenticating state
	if session.State == StateAwaitingQR {
		if session.QrcodeURL != "" {
			logger.Debug("reusing existing QR code for pending login session")
			return &bili.QRCode{Url: session.QrcodeURL}, nil
		}
	}

	if !c.qrcodeHolding.CompareAndSwap(false, true) {
		logger.Warn("已有进行中的二维码登录，已跳过")
		return nil, ErrQRCodeLoginInProgress
	}

	qrcode, err := c.GetQRCode()
	if err != nil {
		wrapped := fmt.Errorf("获取二维码失败：%v", err)
		c.updateSession(func(s *AuthSession) {
			s.State = StateFailed
			s.Error = wrapped
		})
		c.qrcodeHolding.Store(false)
		return nil, wrapped
	}

	session.cancelAutoRefreshCookies() // Cancel any ongoing cookie refresh from previous session, if applicable

	// Each init creates a fresh session and supersedes previous pending QR sessions.
	c.updateSession(func(s *AuthSession) {
		s.State = StateAwaitingQR
		s.QrcodeURL = qrcode.Url
		s.Error = nil
		s.cookieRefreshCancel = nil
	})

	go c.performQRLogin(qrcode.QrcodeKey)

	logger.Info("请扫描二维码登录：")
	return qrcode, nil
}

// performQRLogin is called in a background goroutine to handle the blocking QR login
func (c *Client) performQRLogin(qrcodeKey string) {
	_, _, _ = c.loginSF.Do("bilibili_qr_login_"+qrcodeKey, func() (any, error) {
		defer c.qrcodeHolding.Store(false)

		result, err := c.LoginWithQRCode(bili.LoginWithQRCodeParam{
			QrcodeKey: qrcodeKey,
		})

		if err != nil {
			wrapped := fmt.Errorf("二维码登录出错：%v", err)
			c.updateSession(func(s *AuthSession) {
				s.State = StateFailed
				s.QrcodeURL = ""
				s.Error = wrapped
			})
			logger.Errorf("二维码登录出错：%v", err)
			return nil, nil
		}

		switch result.Code {
		case 86038: // 二维码已失效
			wrapped := fmt.Errorf("登录失败：%s（代码 %d）", result.Message, result.Code)
			c.updateSession(func(s *AuthSession) {
				s.State = StateQRExpired
				s.QrcodeURL = ""
				s.Error = wrapped
			})
			logger.Errorf("登录失败：%s（代码 %d）", result.Message, result.Code)
			return nil, nil
		case 0: // 成功
			// continue
		default:
			wrapped := fmt.Errorf("登录失败：%s（代码 %d）", result.Message, result.Code)
			c.updateSession(func(s *AuthSession) {
				s.State = StateFailed
				s.QrcodeURL = ""
				s.Error = wrapped
			})
			logger.Errorf("登录失败：%s（代码 %d）", result.Message, result.Code)
			return nil, nil
		}

		// Successful login, save credentials and sync
		defer c.syncCookies()

		// Get account info and update state
		if acc, err := c.GetAccountInformation(); err == nil {
			refreshCtx, cancel := context.WithCancel(c.ctx)
			c.updateSession(func(s *AuthSession) {
				s.State = StateAuthenticated
				s.QrcodeURL = ""
				s.Account = acc
				s.Error = nil
				s.cookieRefreshCancel = cancel
			})

			logger.Infof("登录成功，当前账号：%s（mid：%d）", acc.Uname, acc.Mid)

			if err := c.writeRefreshTokenToFile(result.RefreshToken); err != nil {
				logger.Warn(err)
			}

			if err := c.writerCookiesToFile(); err != nil {
				logger.Warn(err)
			}

			go c.refreshCookiesPeriodically(refreshCtx, 10*time.Minute)
		} else {
			wrapped := fmt.Errorf("登录后获取账号信息失败：%v", err)
			c.updateSession(func(s *AuthSession) {
				s.State = StateFailed
				s.QrcodeURL = ""
				s.Error = wrapped
			})
			logger.Errorf("登录后获取账号信息失败：%v", err)
		}

		return nil, nil
	})
}
