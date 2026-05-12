package bilibili

import (
	"time"

	"github.com/go-resty/resty/v2"
)

// GetLiveHlsPlaylistClient returns a lazy-initialized client for fetching m3u8 playlists.
// Uses a short timeout (3s) since playlists are small.
// The client is cached and reused for connection keep-alive.
func (c *Client) GetLiveHlsPlaylistClient() *resty.Client {
	c.hlsPlaylistClientOnce.Do(func() {
		c.liveHlsPlaylistClient = configureLiveClient(
			resty.New().
				SetRedirectPolicy(resty.FlexibleRedirectPolicy(10)).
				SetTimeout(3*time.Second),
			"application/vnd.apple.mpegurl, application/x-mpegURL, */*",
		)
		syncCookieToClient(c.liveHlsPlaylistClient, c.GetCookies())
		ensureBuvid3Cookie(c.liveHlsPlaylistClient)
	})
	return c.liveHlsPlaylistClient
}

// GetLiveHlsSegmentClient returns a lazy-initialized client for fetching segments and maps.
// Uses a longer timeout (20s) to accommodate segment download latency.
// The client is cached and reused for connection keep-alive.
func (c *Client) GetLiveHlsSegmentClient() *resty.Client {
	c.hlsSegmentClientOnce.Do(func() {
		c.liveHlsSegmentClient = configureLiveClient(
			resty.New().
				SetRedirectPolicy(resty.FlexibleRedirectPolicy(10)).
				SetTimeout(20*time.Second),
			"*/*",
		)
		syncCookieToClient(c.liveHlsSegmentClient, c.GetCookies())
		ensureBuvid3Cookie(c.liveHlsSegmentClient)
	})
	return c.liveHlsSegmentClient
}
