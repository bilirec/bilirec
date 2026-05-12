package bilibili

import (
	"context"

	"github.com/go-resty/resty/v2"
)

func (c *Client) FetchLiveStreamUrl(url string) (*resty.Response, error) {
	return c.DoLiveStream(func(req *resty.Request) (*resty.Response, error) {
		return req.Get(url)
	})
}

func (c *Client) FetchLiveStreamUrlWithCtx(url string, ctx context.Context) (*resty.Response, error) {
	return c.DoLiveStream(func(req *resty.Request) (*resty.Response, error) {
		return req.SetContext(ctx).Get(url)
	})
}
