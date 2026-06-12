package stream

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	hlsutil "github.com/bilirec/bilirec/pkg/hls"
	"github.com/go-resty/resty/v2"
	"github.com/sirupsen/logrus"
)

func isCanceled(err error, ctx context.Context) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.Canceled) || ctx.Err() == context.Canceled
}

type hlsSegment = hlsutil.Segment
type hlsPlaylist = hlsutil.Playlist

var (
	ErrM3u8Expired        = errors.New("hls：m3u8 播放列表已过期")
	ErrNoM3u8URL          = errors.New("hls：没有可用的 m3u8 URL")
	playlistRetryDelay    = 250 * time.Millisecond
	playlistRetryAttempts = 2
	segmentRetryDelay     = 500 * time.Millisecond
	segmentRetryAttempts  = 3
	manifestSyncWaitRate  = 0.10
	segmentPrefetchAhead  = 2
)

func desiredPrefetchWorkersForState(baseWorkers int, lowMem bool, consecutiveFailures int) int {
	if lowMem && consecutiveFailures > 0 {
		return 1
	}
	return baseWorkers
}

func resolvePrefetchPolicy() (baseWorkers int, lowMem bool) {
	// Keep stream path safe even in unusual bootstrap/test contexts.
	if config.ReadOnly == nil {
		return 4, false
	}
	return config.ReadOnly.HlsSegmentFetchWorkers(), config.ReadOnly.IsLowMemPreset()
}

type playlistStatusError struct {
	status int
}

func (e *playlistStatusError) Error() string {
	return fmt.Sprintf("m3u8 状态码 %d", e.status)
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

func isRetryablePlaylistFetchErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}

	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "timeout") ||
		strings.Contains(errText, "connection reset") ||
		strings.Contains(errText, "broken pipe") ||
		strings.Contains(errText, "goaway") ||
		strings.Contains(errText, "eof")
}

// ReadHlsStream polls an HLS m3u8 playlist and delivers each new segment as a
// complete []byte to the returned channel. One send = one full TS or fMP4
// segment, ready to be written to disk.
//
// The polling interval is derived from #EXT-X-TARGETDURATION when present,
// falling back to the first EXTINF duration and then 1 second.
//
// playlistClient should have a short timeout for m3u8 fetches.
// segmentClient should have a longer timeout for segment and map downloads.
func (r *Service) ReadHlsStream(fetchM3u8URL func() (string, error), playlistClient, segmentClient *resty.Client, ctx context.Context) (<-chan []byte, error) {
	var (
		m3u8URL        string
		resolver       *hlsutil.URLResolver
		prefetcher     *hlsutil.SegmentPrefetcher
		currentMapURI  string
		mapSent        bool
		lastEtag       string
		lastModified   string
		cachedPlaylist *hlsPlaylist
		prefetchWorkers int
	)

	refreshM3u8URL := func(reason string) error {
		nextURL, err := fetchM3u8URL()
		if err != nil {
			return fmt.Errorf("hls：无法获取 m3u8 URL（%s）：%w", reason, err)
		}
		nextURL = strings.TrimSpace(nextURL)
		if nextURL == "" {
			return ErrNoM3u8URL
		}

		nextResolver, err := hlsutil.NewURLResolver(nextURL)
		if err != nil {
			return fmt.Errorf("hls：刷新后 m3u8 URL 无效（%s）：%w", reason, err)
		}

		if m3u8URL != "" && m3u8URL != nextURL {
			logger.Warnf("hls：由于 %s 已刷新 m3u8 URL", reason)
		}

		m3u8URL = nextURL
		resolver = nextResolver
		prefetcher = nil
		prefetchWorkers = 0
		currentMapURI = ""
		mapSent = false
		lastEtag = ""
		lastModified = ""
		cachedPlaylist = nil
		return nil
	}

	if err := refreshM3u8URL("initial"); err != nil {
		if errors.Is(err, ErrNoM3u8URL) {
			return nil, ErrNoM3u8URL
		}
		return nil, fmt.Errorf("hls：无法获取初始 m3u8 URL：%w", err)
	}

	fetchPlaylist := func() (*hlsPlaylist, error) {
		for attempt := 1; attempt <= playlistRetryAttempts; attempt++ {
			req := playlistClient.R().SetContext(ctx)
			if lastEtag != "" {
				req.SetHeader("If-None-Match", lastEtag)
			}
			if lastModified != "" {
				req.SetHeader("If-Modified-Since", lastModified)
			}
			resp, err := req.Get(m3u8URL)
			if err != nil {
				if attempt < playlistRetryAttempts && isRetryablePlaylistFetchErr(err) && !isCanceled(err, ctx) {
					logger.Warnf("hls：获取 m3u8 失败，正在进行第 %d/%d 次重试：%v", attempt+1, playlistRetryAttempts, err)
					timer := time.NewTimer(playlistRetryDelay)
					select {
					case <-timer.C:
					case <-ctx.Done():
						timer.Stop()
						return nil, ctx.Err()
					}
					continue
				}
				return nil, fmt.Errorf("获取 m3u8 失败：%w", err)
			}

			if hlsutil.IsM3u8URLExpiredStatus(resp.StatusCode()) {
				return nil, fmt.Errorf("%w (status=%d)", ErrM3u8Expired, resp.StatusCode())
			} else if resp.StatusCode() == 304 {
				if cachedPlaylist != nil {
					return cachedPlaylist, nil
				}
				return nil, fmt.Errorf("m3u8 状态码 %d 且无可用缓存", resp.StatusCode())
			} else if resp.StatusCode() != 200 {
				if resp.StatusCode() >= 500 && resp.StatusCode() < 600 {
					return nil, &playlistStatusError{status: resp.StatusCode()}
				}
				return nil, fmt.Errorf("m3u8 状态码 %d", resp.StatusCode())
			}

			pl, parseErr := hlsutil.ParseBytes(resp.Body())
			if parseErr != nil {
				return nil, fmt.Errorf("解析 m3u8 失败：%w", parseErr)
			}
			lastEtag = resp.Header().Get("Etag")
			lastModified = resp.Header().Get("Last-Modified")
			cachedPlaylist = pl
			return pl, nil
		}

		return nil, fmt.Errorf("获取 m3u8 失败：重试次数已耗尽")
	}

	fetchPlaylistWithRefresh := func() (*hlsPlaylist, error) {
		refreshAttempts := 0
		for {
			pl, err := fetchPlaylist()
			if err == nil {
				return pl, nil
			}
			if isCanceled(err, ctx) || ctx.Err() == context.Canceled {
				return nil, err
			}
			if !isM3u8URLExpiredErr(err) && !isRetryablePlaylistStatusErr(err) {
				return nil, err
			}
			if refreshAttempts >= 1 {
				return nil, err
			}

			refreshErr := refreshM3u8URL(err.Error())
			if refreshErr != nil {
				return nil, refreshErr
			}
			refreshAttempts++
			logger.Warnf("hls：播放列表异常后已刷新 m3u8 URL（原因：%v），正在重试拉取播放列表", err)
		}
	}

	// Fetch the initial playlist to verify reachability and derive poll interval.
	pl, err := fetchPlaylistWithRefresh()
	if err != nil {
		if errors.Is(err, ErrNoM3u8URL) {
			return nil, ErrNoM3u8URL
		}
		return nil, fmt.Errorf("hls：无法解析初始 m3u8：%w", err)
	}
	mediaSeq, segs := pl.MediaSeq, pl.Segments

	pollInterval := hlsutil.DerivePollInterval(pl)
	logger.Infof("hls：轮询间隔=%v（target=%.2fs，first-extinf=%.2fs）", pollInterval, func() float64 {
		if len(segs) > 0 {
			return pl.TargetDuration
		}
		return 0
	}(), func() float64 {
		if len(segs) > 0 {
			return segs[0].Duration
		}
		return 0
	}())

	// nextSeq is the sequence number of the next segment we want to fetch.
	// For fMP4 playlists (EXT-X-MAP present), start from the current window head
	// to improve startup decode stability (higher chance to include an IDR GOP).
	// For TS playlists, keep the old behavior and start from the window tail.
	nextSeq := mediaSeq + int64(len(segs))
	if pl.MapURI != "" {
		nextSeq = mediaSeq
	}
	currentMapURI = pl.MapURI
	mapSent = false

	ch := make(chan []byte, r.chanBufferSize)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		syncWaitTimer := time.NewTimer(time.Hour)
		if !syncWaitTimer.Stop() {
			select {
			case <-syncWaitTimer.C:
			default:
			}
		}
		defer syncWaitTimer.Stop()
		consecutivePlaylistFailures := 0
		prevBaseSeq := mediaSeq
		lastSyncWaitBaseSeq := int64(-1)
		traceEnabled := logger.Logger.IsLevelEnabled(logrus.TraceLevel)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pl, err := fetchPlaylistWithRefresh()
				if err != nil {
					if errors.Is(err, ErrNoM3u8URL) {
						logger.Infof("hls：已无可用 m3u8 URL，直播可能已结束")
						return
					}
					if isCanceled(err, ctx) || ctx.Err() == context.Canceled {
						return
					}
					consecutivePlaylistFailures++
					logger.Warnf("hls：拉取/解析播放列表失败（第 %d 次）：%v", consecutivePlaylistFailures, err)
					// Drop prefetcher references aggressively on failure boundaries.
					prefetcher = nil
					prefetchWorkers = 0

					// Retry once immediately to reduce the chance of missing short HLS windows.
					pl, err = fetchPlaylistWithRefresh()
					if err != nil {
						if errors.Is(err, ErrNoM3u8URL) {
							logger.Infof("hls：已无可用 m3u8 URL，直播可能已结束")
							return
						}
						if isCanceled(err, ctx) || ctx.Err() == context.Canceled {
							return
						}
						consecutivePlaylistFailures++
						logger.Warnf("hls：立即重试播放列表失败（第 %d 次）：%v", consecutivePlaylistFailures, err)
						prefetcher = nil
						prefetchWorkers = 0
						if consecutivePlaylistFailures >= 3 {
							logger.Warnf("hls：播放列表连续失败次数达到 %d", consecutivePlaylistFailures)
							return
						}
						continue
					}

					logger.Warn("hls：通过立即重试恢复了播放列表")
				}

				if consecutivePlaylistFailures > 0 {
					logger.Infof("hls：在 %d 次失败后拉取/解析播放列表已恢复", consecutivePlaylistFailures)
				}
				consecutivePlaylistFailures = 0

				updatedPollInterval := hlsutil.DerivePollInterval(pl)
				if updatedPollInterval != pollInterval {
					logger.Infof("hls：轮询间隔已从 %v 更新为 %v（target=%.2fs，first-extinf=%.2fs）", pollInterval, updatedPollInterval, pl.TargetDuration, func() float64 {
						if len(pl.Segments) > 0 {
							return pl.Segments[0].Duration
						}
						return 0
					}())
					pollInterval = updatedPollInterval
					ticker.Reset(pollInterval)
				}

				baseSeq, segs := pl.MediaSeq, pl.Segments
				if traceEnabled {
					pendingSegments := hlsutil.CountPendingSegments(baseSeq, segs, nextSeq)
					logger.Tracef("hls: playlist window base=%d len=%d next=%d pending=%d map=%t", baseSeq, len(segs), nextSeq, pendingSegments, pl.MapURI != "")
				}

				if baseSeq > nextSeq {
					lost := baseSeq - nextSeq
					logger.Warnf("hls：检测到序列间隙，可能丢失了 %d 个分片（nextSeq=%d，baseSeq=%d）", lost, nextSeq, baseSeq)
					nextSeq = baseSeq
				}
				if hlsutil.ShouldResetSequenceOnRollback(prevBaseSeq, baseSeq, len(segs), nextSeq) {
					logger.Warnf("hls：检测到序列回退/不连续（nextSeq=%d，baseSeq=%d，window=%d），正在重置 nextSeq", nextSeq, baseSeq, len(segs))
					nextSeq = baseSeq
					mapSent = false
				}

				if pl.MapURI != currentMapURI {
					currentMapURI = pl.MapURI
					mapSent = false
				}

				if len(segs) > 0 && hlsutil.ShouldApplyManifestSyncWait(baseSeq, nextSeq, lastSyncWaitBaseSeq) {
					waitForSync := hlsutil.DeriveManifestSyncWait(pl, manifestSyncWaitRate)
					logger.Debugf("hls: applying manifest sync wait=%v base=%d next=%d", waitForSync, baseSeq, nextSeq)
					syncWaitTimer.Reset(waitForSync)
					select {
					case <-syncWaitTimer.C:
						lastSyncWaitBaseSeq = baseSeq
						logger.Debugf("hls: manifest sync wait completed base=%d", baseSeq)
					case <-ctx.Done():
						if !syncWaitTimer.Stop() {
							select {
							case <-syncWaitTimer.C:
							default:
							}
						}
						return
					}
				}
				baseWorkers, lowMem := resolvePrefetchPolicy()
				desiredPrefetchWorkers := desiredPrefetchWorkersForState(baseWorkers, lowMem, consecutivePlaylistFailures)
				if prefetcher == nil || prefetchWorkers != desiredPrefetchWorkers {
					if prefetchWorkers > 0 && prefetchWorkers != desiredPrefetchWorkers {
						logger.Infof("hls：预取并发已从 %d 调整为 %d", prefetchWorkers, desiredPrefetchWorkers)
					}
					prefetcher = hlsutil.NewSegmentPrefetcher(ctx, segmentClient, resolver, segmentRetryAttempts, segmentRetryDelay, desiredPrefetchWorkers)
					prefetchWorkers = desiredPrefetchWorkers
				}
				// Defensive: make sure downstream Start/Wait never touches a nil prefetcher.
				if prefetcher == nil {
					logger.Warnf("hls：prefetcher 为空，回退重建（workers=%d）", desiredPrefetchWorkers)
					prefetcher = hlsutil.NewSegmentPrefetcher(ctx, segmentClient, resolver, segmentRetryAttempts, segmentRetryDelay, desiredPrefetchWorkers)
					prefetchWorkers = desiredPrefetchWorkers
				}
				maxSeqInWindow := baseSeq + int64(len(segs)) - 1
				if nextSeq <= maxSeqInWindow {
					primeEndSeq := nextSeq + int64(segmentPrefetchAhead)
					if primeEndSeq > maxSeqInWindow {
						primeEndSeq = maxSeqInWindow
					}
					for seq := nextSeq; seq <= primeEndSeq; seq++ {
						idx := int(seq - baseSeq)
						prefetcher.Start(seq, segs[idx].URI)
					}
				}
				nextPrefetchSeq := nextSeq + int64(segmentPrefetchAhead) + 1

				for i, seg := range segs {
					segSeq := baseSeq + int64(i)
					if segSeq < nextSeq {
						continue // already downloaded
					}

					if currentMapURI != "" && !mapSent {
						mapFetchStart := time.Now()
						mapURL, err := resolver.Resolve(currentMapURI)
						if err != nil {
							logger.Warnf("hls：无法解析 map URL %q：%v", currentMapURI, err)
							continue
						}

						mapResp, err := segmentClient.R().SetContext(ctx).Get(mapURL)
						if err != nil {
							if isCanceled(err, ctx) {
								return
							}
							logger.Warnf("hls：拉取 map 失败：%v", err)
							continue
						}
						if mapResp.StatusCode() != 200 {
							logger.Warnf("hls：map 状态码 %d", mapResp.StatusCode())
							continue
						}
						logger.Debugf("hls: map fetch ok bytes=%d elapsed=%v", len(mapResp.Body()), time.Since(mapFetchStart))

						select {
						case ch <- mapResp.Body():
							mapSent = true
						case <-ctx.Done():
							return
						}
					}

					if nextPrefetchSeq <= maxSeqInWindow {
						prefetchIdx := int(nextPrefetchSeq - baseSeq)
						if traceEnabled {
							logger.Tracef("hls: prefetch start seq=%d uri=%s", nextPrefetchSeq, segs[prefetchIdx].URI)
						}
						prefetcher.Start(nextPrefetchSeq, segs[prefetchIdx].URI)
						nextPrefetchSeq++
					}

					waitStart := time.Now()
					data, err := prefetcher.Wait(segSeq, seg.URI)
					if err != nil {
						if isCanceled(err, ctx) || ctx.Err() == context.Canceled {
							return
						}
						logger.Warnf("hls：拉取分片失败（seq=%d）：%v", segSeq, err)
						continue
					}
					if traceEnabled {
						logger.Tracef("hls: segment ready seq=%d bytes=%d wait=%v", segSeq, len(data), time.Since(waitStart))
					}

					nextSeq = segSeq + 1

					select {
					case ch <- data:
					case <-ctx.Done():
						return
					}
				}

				prevBaseSeq = baseSeq
			}
		}
	}()

	return ch, nil
}
