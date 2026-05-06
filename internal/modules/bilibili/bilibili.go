package bilibili

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"go.uber.org/fx"

	bili "github.com/CuteReimu/bilibili/v2"
	"github.com/eric2788/bilirec/internal/modules/config"
	"github.com/eric2788/bilirec/utils"
	"github.com/go-resty/resty/v2"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/singleflight"
)

var logger = logrus.WithField("module", "bilibili")

var (
	ErrRoomNotFound = errors.New("房间不存在")
)

const liveReferer = "https://live.bilibili.com/"
const liveOrigin = "https://live.bilibili.com"
const liveUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0"

type Client struct {
	*bili.Client

	loginMode        string
	refreshToken     string
	wbi              *bili.WBI
	liveClient       *resty.Client
	liveStreamClient *resty.Client
	ctx              context.Context

	cookiePath       string
	refreshTokenPath string

	// optional client
	hlsPlaylistClientOnce sync.Once
	liveHlsPlaylistClient *resty.Client
	hlsSegmentClientOnce  sync.Once
	liveHlsSegmentClient  *resty.Client

	// Auth session management for controller mode
	session atomic.Pointer[AuthSession]
	loginSF singleflight.Group
}

func provider(cfg *config.Config, ls fx.Lifecycle) *Client {

	ctx, cancel := context.WithCancel(context.Background())

	client := utils.TernaryFunc(
		cfg.AnonymousLogin,
		func() *Client {
			return &Client{Client: bili.NewAnonymousClient()}
		},
		func() *Client {
			return &Client{Client: bili.New()}
		},
	)

	client.ctx = ctx
	client.loginMode = cfg.BilibiliLoginOn
	client.wbi = bili.NewDefaultWbi()
	client.liveClient = client.withLiveClient()
	client.liveStreamClient = client.withLiveStreamClient()
	client.cookiePath = fmt.Sprintf("%s%c_cookies", cfg.SecretDir, os.PathSeparator)
	client.refreshTokenPath = fmt.Sprintf("%s%c_refresh_token", cfg.SecretDir, os.PathSeparator)
	client.session.Store(&AuthSession{State: StateIdle})

	ls.Append(
		fx.StartStopHook(
			func() error {
				if cfg.AnonymousLogin {
					logger.Info("using anonymous login, skipping bilibili login process")
					return nil
				}

				// Handle login timing based on config
				if cfg.BilibiliLoginOn == "controller" {
					// Controller mode: preload only, don't block on QR
					logger.Info("bilibili login mode: controller (preload only)")
					return client.preloadCookies()
				} else if cfg.BilibiliLoginOn == "startup" {
					// Startup mode (default): full login flow at startup
					logger.Info("starting bilibili login process")
					return client.loadCookiesOrLogin()
				} else {
					return fmt.Errorf("unknown bilibili login mode: %s", cfg.BilibiliLoginOn)
				}
			},
			func() error {
				cancel()
				return nil
			},
		),
	)

	return client
}

func (c *Client) withLiveClient() *resty.Client {
	return configureLiveClient(resty.New().SetRedirectPolicy(resty.NoRedirectPolicy()), "application/json")
}

func (c *Client) withLiveStreamClient() *resty.Client {
	return configureLiveClient(
		resty.New().
			SetRedirectPolicy(resty.FlexibleRedirectPolicy(10)).
			SetDoNotParseResponse(true),
		"*/*",
	)
}

func configureLiveClient(client *resty.Client, accept string) *resty.Client {
	return client.
		SetHeader("Accept", accept).
		SetHeader("Accept-Language", "zh-CN,zh;q=0.9").
		SetHeader("Origin", liveOrigin).
		SetHeader("Referer", liveReferer).
		SetHeader("User-Agent", liveUserAgent)
}

func ensureBuvid3Cookie(client *resty.Client) {
	for _, cookie := range client.Cookies {
		if cookie.Name == "buvid3" && cookie.Value != "" {
			return
		}
	}

	uuid, err := utils.NewUUIDv4()
	if err != nil {
		logger.Warnf("生成 buvid3 Cookie 失败：%v", err)
		return
	}

	client.Cookies = append(client.Cookies, &http.Cookie{
		Name:   "buvid3",
		Value:  strings.ToUpper(uuid) + "infoc",
		Domain: ".bilibili.com",
		Path:   "/",
	})
}

var Module = fx.Module("bilibili",
	fx.Provide(provider),
)
