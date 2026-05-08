package bilibili

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"go.uber.org/fx"

	bili "github.com/CuteReimu/bilibili/v2"
	"github.com/eric2788/bilirec/internal/modules/config"
	"github.com/eric2788/bilirec/utils"
	"github.com/go-resty/resty/v2"
	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("module", "bilibili")

var (
	ErrRoomNotFound = errors.New("room not found")
)

const liveReferer = "https://live.bilibili.com/"
const liveOrigin = "https://live.bilibili.com"
const liveUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0"

type Client struct {
	*bili.Client
	refreshToken     string
	wbi              *bili.WBI
	liveClient       *resty.Client
	liveStreamClient *resty.Client

	cookiePath       string
	refreshTokenPath string
}

func provider(cfg *config.Config, ls fx.Lifecycle) *Client {

	client := utils.TernaryFunc(
		cfg.AnonymousLogin,
		func() *Client {
			return &Client{Client: bili.NewAnonymousClient()}
		},
		func() *Client {
			return &Client{Client: bili.New()}
		},
	)

	client.wbi = bili.NewDefaultWbi()
	client.liveClient = client.withLiveClient()
	client.liveStreamClient = client.withLiveStreamClient()
	client.cookiePath = fmt.Sprintf("%s%c_cookies", cfg.SecretDir, os.PathSeparator)
	client.refreshTokenPath = fmt.Sprintf("%s%c_refresh_token", cfg.SecretDir, os.PathSeparator)

	ls.Append(
		fx.StartHook(func(ctx context.Context) error {
			return client.loadCookiesOrLogin(ctx, cfg)
		}),
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

func (c *Client) NewLiveHlsClient() *resty.Client {
	client := configureLiveClient(resty.New().SetRedirectPolicy(resty.FlexibleRedirectPolicy(10)), "*/*")
	syncCookieToClient(client, c.GetCookies())
	ensureBuvid3Cookie(client)
	return client
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
		logger.Warnf("cannot generate buvid3 cookie: %v", err)
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
