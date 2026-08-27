package hls

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"

	"github.com/go-resty/resty/v2"
)

var (
	// ErrM3u8Expired is returned when the playlist URL is no longer valid.
	ErrM3u8Expired = errors.New("hls：m3u8 播放列表已过期")
	// ErrNoM3u8URL is returned when no playlist URL is available.
	ErrNoM3u8URL = errors.New("hls：没有可用的 m3u8 URL")

	// Tunables (tests may shorten delays).
	PlaylistRetryDelay         = 250 * time.Millisecond
	PlaylistRetryAttempts      = 2
	SegmentRetryDelay          = 500 * time.Millisecond
	SegmentRetryAttempts       = 3
	ManifestSyncWaitRate       = 0.10
	SegmentPrefetchAhead       = 2
	SegmentFetchWorkers        = 4
	SegmentFailureRefreshEvery = 3
)

type playlistStatusError struct {
	status int
}

func (e *playlistStatusError) Error() string {
	return fmt.Sprintf("m3u8 状态码 %d", e.status)
}

func isCanceled(err error, ctx context.Context) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.Canceled) || ctx.Err() == context.Canceled
}

func isM3u8URLExpiredErr(err error) bool {
	return errors.Is(err, ErrM3u8Expired)
}

func isRetryablePlaylistStatusErr(err error) bool {
	var statusErr *playlistStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	return statusErr.status >= 500 && statusErr.status < 600
}

// PlaylistSessionOptions configures NewPlaylistSession.
type PlaylistSessionOptions struct {
	FetchURL       func() (string, error)
	PlaylistClient *resty.Client
	SegmentClient  *resty.Client
	ReadBody       SegmentBodyReader
	ReleaseBytes   BytesReleaser
	Log            logger.Logger
	OnURLRefresh   func()
}

// PlaylistSession owns m3u8 URL refresh, ETag cache, and playlist HTTP fetch.
// FetchURL is injected by the caller (e.g. bilibili stream URL refresh).
type PlaylistSession struct {
	ctx            context.Context
	fetchURL       func() (string, error)
	playlistClient *resty.Client
	segmentClient  *resty.Client
	readBody       SegmentBodyReader
	release        BytesReleaser
	log            logger.Logger
	onURLRefresh   func()

	m3u8URL        string
	resolver       *URLResolver
	prefetcher     *SegmentPrefetcher
	lastEtag       string
	lastModified   string
	cachedPlaylist *Playlist
}

// NewPlaylistSession creates a playlist session. Call RefreshURL before Fetch.
func NewPlaylistSession(ctx context.Context, opt PlaylistSessionOptions) (*PlaylistSession, error) {
	if opt.FetchURL == nil || opt.PlaylistClient == nil || opt.SegmentClient == nil {
		return nil, fmt.Errorf("hls：PlaylistSession 缺少必要参数")
	}
	if opt.ReadBody == nil {
		opt.ReadBody = readSegmentBody
	}
	if opt.ReleaseBytes == nil {
		opt.ReleaseBytes = func([]byte) {}
	}
	return &PlaylistSession{
		ctx:            ctx,
		fetchURL:       opt.FetchURL,
		playlistClient: opt.PlaylistClient,
		segmentClient:  opt.SegmentClient,
		readBody:       opt.ReadBody,
		release:        opt.ReleaseBytes,
		log:            opt.Log,
		onURLRefresh:   opt.OnURLRefresh,
	}, nil
}

// SetOnURLRefresh replaces the URL-refresh hook (e.g. InitSettle.Reset).
func (s *PlaylistSession) SetOnURLRefresh(fn func()) {
	s.onURLRefresh = fn
}

// Resolver returns the current URL resolver (nil before RefreshURL).
func (s *PlaylistSession) Resolver() *URLResolver { return s.resolver }

// Prefetcher returns the current segment prefetcher (nil before RefreshURL).
func (s *PlaylistSession) Prefetcher() *SegmentPrefetcher { return s.prefetcher }

// RefreshURL asks FetchURL for a playlist URL and rebuilds resolver/prefetcher.
func (s *PlaylistSession) RefreshURL(reason string) error {
	nextURL, err := s.fetchURL()
	if err != nil {
		return fmt.Errorf("hls：无法获取 m3u8 URL（%s）：%w", reason, err)
	}
	nextURL = strings.TrimSpace(nextURL)
	if nextURL == "" {
		return ErrNoM3u8URL
	}

	nextResolver, err := NewURLResolver(nextURL)
	if err != nil {
		return fmt.Errorf("hls：刷新后 m3u8 URL 无效（%s）：%w", reason, err)
	}

	if s.m3u8URL != "" && s.m3u8URL != nextURL {
		s.log.Warnf("hls：由于 %s 已刷新 m3u8 URL", reason)
	}

	if s.prefetcher != nil {
		s.prefetcher.Abandon()
	}
	s.m3u8URL = nextURL
	s.resolver = nextResolver
	s.prefetcher = NewSegmentPrefetcher(s.ctx, s.segmentClient, s.resolver, SegmentRetryAttempts, SegmentRetryDelay, SegmentFetchWorkers, s.readBody, s.release)
	s.lastEtag = ""
	s.lastModified = ""
	s.cachedPlaylist = nil
	if s.onURLRefresh != nil {
		s.onURLRefresh()
	}
	return nil
}

// Fetch downloads and parses the current playlist (with retries / ETag).
func (s *PlaylistSession) Fetch() (*Playlist, error) {
	for attempt := 1; attempt <= PlaylistRetryAttempts; attempt++ {
		req := s.playlistClient.R().SetContext(s.ctx)
		if s.lastEtag != "" {
			req.SetHeader("If-None-Match", s.lastEtag)
		}
		if s.lastModified != "" {
			req.SetHeader("If-Modified-Since", s.lastModified)
		}
		resp, err := req.Get(s.m3u8URL)
		if err != nil {
			if attempt < PlaylistRetryAttempts && IsRetryableFetchErr(err) && !isCanceled(err, s.ctx) {
				s.log.Warnf("hls：获取 m3u8 失败，正在进行第 %d/%d 次重试：%v", attempt+1, PlaylistRetryAttempts, err)
				timer := time.NewTimer(PlaylistRetryDelay)
				select {
				case <-timer.C:
				case <-s.ctx.Done():
					timer.Stop()
					return nil, s.ctx.Err()
				}
				continue
			}
			return nil, fmt.Errorf("获取 m3u8 失败：%w", err)
		}

		if IsM3u8URLExpiredStatus(resp.StatusCode()) {
			return nil, fmt.Errorf("%w (status=%d)", ErrM3u8Expired, resp.StatusCode())
		} else if resp.StatusCode() == 304 {
			if s.cachedPlaylist != nil {
				return s.cachedPlaylist, nil
			}
			return nil, fmt.Errorf("m3u8 状态码 %d 且无可用缓存", resp.StatusCode())
		} else if resp.StatusCode() != 200 {
			if resp.StatusCode() >= 500 && resp.StatusCode() < 600 {
				return nil, &playlistStatusError{status: resp.StatusCode()}
			}
			return nil, fmt.Errorf("m3u8 状态码 %d", resp.StatusCode())
		}

		pl, parseErr := ParseBytes(resp.Body())
		if parseErr != nil {
			return nil, fmt.Errorf("解析 m3u8 失败：%w", parseErr)
		}
		s.lastEtag = resp.Header().Get("Etag")
		s.lastModified = resp.Header().Get("Last-Modified")
		s.cachedPlaylist = pl
		return pl, nil
	}
	return nil, fmt.Errorf("获取 m3u8 失败：重试次数已耗尽")
}

// FetchWithRefresh fetches the playlist and refreshes the URL once on expiry/5xx.
func (s *PlaylistSession) FetchWithRefresh() (*Playlist, error) {
	refreshAttempts := 0
	for {
		pl, err := s.Fetch()
		if err == nil {
			return pl, nil
		}
		if isCanceled(err, s.ctx) || s.ctx.Err() == context.Canceled {
			return nil, err
		}
		if !isM3u8URLExpiredErr(err) && !isRetryablePlaylistStatusErr(err) {
			return nil, err
		}
		if refreshAttempts >= 1 {
			return nil, err
		}
		if refreshErr := s.RefreshURL(err.Error()); refreshErr != nil {
			return nil, refreshErr
		}
		refreshAttempts++
		s.log.Warnf("hls：播放列表异常后已刷新 m3u8 URL（原因：%v），正在重试拉取播放列表", err)
	}
}
