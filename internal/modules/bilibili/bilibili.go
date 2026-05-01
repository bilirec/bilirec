package bilibili

import (
	"context"
	"errors"
	"fmt"
	"os"

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
	return resty.New().
		SetRedirectPolicy(resty.NoRedirectPolicy()).
		SetHeader("Accept", "application/json").
		SetHeader("Accept-Language", "zh-CN,zh;q=0.9").
		SetHeader("Origin", "https://live.bilibili.com").
		SetHeader("Referer", "https://live.bilibili.com/").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0")
}

func (c *Client) withLiveStreamClient() *resty.Client {
	return resty.New().
		SetRedirectPolicy(resty.FlexibleRedirectPolicy(10)).
		SetDoNotParseResponse(true).
		SetHeader("Accept", "*/*").
		SetHeader("Accept-Language", "zh-CN,zh;q=0.9").
		SetHeader("Origin", "https://live.bilibili.com").
		SetHeader("Referer", "https://live.bilibili.com/").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0")
}

var Module = fx.Module("bilibili",
	fx.Provide(provider),
)
