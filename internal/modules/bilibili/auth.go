package bilibili

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	bili "github.com/CuteReimu/bilibili/v2"
	"github.com/eric2788/bilirec/internal/modules/config"
	"github.com/go-resty/resty/v2"
)

func (c *Client) loadCookiesOrLogin(ctx context.Context, cfg *config.Config) error {
	if cfg.AnonymousLogin {
		logger.Info("使用匿名登录，跳过哔哩哔哩登录流程")
		return nil
	}

	defer c.syncCookies()

	if cookie, token, err := c.loadOfflineCredentials(); err == nil {
		c.SetCookiesString(cookie)
		c.wbi.WithCookies(c.GetCookies())
		c.refreshToken = token

		if acc, err := c.GetAccountInformation(); err == nil {
			logger.Infof("已加载用户 Cookie：%s（mid：%d）", acc.Uname, acc.Mid)
			return c.refreshCookiesIfRequired()
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
	}

	if err := c.writeRefreshTokenToFile(result.RefreshToken); err != nil {
		return err
	}
	go c.refreshCookiesPeriodically(ctx, 10*time.Minute)
	return c.writerCookiesToFile()
}

func (c *Client) refreshCookiesIfRequired() error {
	info, err := c.GetWebCookieRefreshInfo()
	if err != nil {
		return fmt.Errorf("获取 cookie 刷新信息失败：%v", err)
	}
	if !info.Refresh {
		logger.Info("Cookie 无需刷新")
		return nil
	}
	csrfResult, err := c.GetWebCookieRefreshCsrf(bili.GetWebCookieRefreshCsrfParam{
		Timestamp: info.Timestamp,
	})
	if err != nil {
		return fmt.Errorf("获取 cookie 刷新 csrf 失败：%v", err)
	}
	refreshed, err := c.RefreshCookie(bili.RefreshCookieParam{
		RefreshToken: c.refreshToken,
		RefreshCsrf:  csrfResult.RefreshCsrf,
	})
	if err != nil {
		return fmt.Errorf("刷新 cookie 失败：%v", err)
	} else {
		logger.Info("Cookie 刷新成功")
		c.syncCookies()
	}

	if err := c.writeRefreshTokenToFile(refreshed.RefreshToken); err != nil {
		return err
	}
	return c.writerCookiesToFile()
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
	// 同樣處理 liveStreamClient
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
