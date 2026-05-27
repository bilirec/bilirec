package cloudconvert

import (
	"context"
	"crypto/tls"
	"net/http"
	"time"

	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/go-resty/resty/v2"
)

type Option func(*Client)

type Client struct {
	client       *resty.Client
	streamClient *http.Client
	ctx          context.Context

	uploadPool *pool.BytesPool
}

func NewClient(ctx context.Context, apiKey string, opts ...Option) *Client {

	client := resty.New().
		SetBaseURL("https://api.cloudconvert.com/v2/").
		SetHeader("Content-Type", "application/json").
		SetRedirectPolicy(resty.FlexibleRedirectPolicy(5)).
		SetTimeout(30 * time.Second).
		SetAuthScheme("Bearer").
		SetAuthToken(apiKey)

	streamClient := &http.Client{
		Timeout: 0, // No timeout for streaming client
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				MaxVersion: tls.VersionTLS12,
				CipherSuites: []uint16{
					tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
					tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
				},
			},
		},
	}

	c := &Client{
		client:       client,
		streamClient: streamClient,
		ctx:          ctx,
		uploadPool:   pool.NewBytesPool(pool.DefaultBufferSize),
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

func WithUploadBufferSize(size int) Option {
	return func(c *Client) {
		c.uploadPool = pool.NewBytesPool(size)
	}
}
