package stream

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	hlsutil "github.com/eric2788/bilirec/pkg/hls"
	"github.com/go-resty/resty/v2"
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
	ErrM3u8Expired       = errors.New("hls: m3u8 playlist expired")
	ErrNoM3u8URL         = errors.New("hls: no m3u8 url available")
	segmentRetryDelay    = 500 * time.Millisecond
	segmentRetryAttempts = 3
	manifestSyncWaitRate = 0.10
	segmentPrefetchAhead = 2
)

func isM3u8URLExpiredErr(err error) bool {
	return errors.Is(err, ErrM3u8Expired)
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
		m3u8URL       string
		resolver      *hlsutil.URLResolver
		currentMapURI string
		mapSent       bool
	)

	refreshM3u8URL := func(reason string) error {
		nextURL, err := fetchM3u8URL()
		if err != nil {
			return fmt.Errorf("hls: cannot fetch m3u8 URL (%s): %w", reason, err)
		}
		nextURL = strings.TrimSpace(nextURL)
		if nextURL == "" {
			return ErrNoM3u8URL
		}

		nextResolver, err := hlsutil.NewURLResolver(nextURL)
		if err != nil {
			return fmt.Errorf("hls: invalid m3u8 URL after refresh (%s): %w", reason, err)
		}

		if m3u8URL != "" && m3u8URL != nextURL {
			logger.Warnf("hls: refreshed m3u8 URL due to %s", reason)
		}

		m3u8URL = nextURL
		resolver = nextResolver
		currentMapURI = ""
		mapSent = false
		return nil
	}

	if err := refreshM3u8URL("initial"); err != nil {
		if errors.Is(err, ErrNoM3u8URL) {
			return nil, ErrNoM3u8URL
		}
		return nil, fmt.Errorf("hls: cannot fetch initial m3u8 URL: %w", err)
	}

	fetchPlaylist := func() (*hlsPlaylist, error) {
		resp, err := playlistClient.R().SetContext(ctx).Get(m3u8URL)
		if err != nil {
			return nil, fmt.Errorf("fetch m3u8: %w", err)
		}
		if hlsutil.IsM3u8URLExpiredStatus(resp.StatusCode()) {
			return nil, fmt.Errorf("%w (status=%d)", ErrM3u8Expired, resp.StatusCode())
		} else if resp.StatusCode() != 200 {
			return nil, fmt.Errorf("m3u8 status %d", resp.StatusCode())
		}
		pl, err := hlsutil.ParseBytes(resp.Body())
		if err != nil {
			return nil, fmt.Errorf("parse m3u8: %w", err)
		}
		return pl, nil
	}

	fetchPlaylistWithRefresh := func() (*hlsPlaylist, error) {
		for {
			pl, err := fetchPlaylist()
			if err == nil {
				return pl, nil
			}
			if isCanceled(err, ctx) || ctx.Err() == context.Canceled {
				return nil, err
			}
			if !isM3u8URLExpiredErr(err) {
				return nil, err
			}

			refreshErr := refreshM3u8URL(err.Error())
			if refreshErr != nil {
				return nil, refreshErr
			}
			logger.Warn("hls: refreshed m3u8 URL after playlist expiration, retrying playlist fetch")
		}
	}

	// Fetch the initial playlist to verify reachability and derive poll interval.
	pl, err := fetchPlaylistWithRefresh()
	if err != nil {
		if errors.Is(err, ErrNoM3u8URL) {
			return nil, ErrNoM3u8URL
		}
		return nil, fmt.Errorf("hls: cannot parse initial m3u8: %w", err)
	}
	mediaSeq, segs := pl.MediaSeq, pl.Segments

	pollInterval := hlsutil.DerivePollInterval(pl)
	logger.Infof("hls: poll interval=%v (target=%.2fs, first-extinf=%.2fs)", pollInterval, func() float64 {
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

	ch := make(chan []byte, 5)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		consecutivePlaylistFailures := 0
		prevBaseSeq := mediaSeq
		lastSyncWaitBaseSeq := int64(-1)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pl, err := fetchPlaylistWithRefresh()
				if err != nil {
					if errors.Is(err, ErrNoM3u8URL) {
						logger.Infof("hls: no m3u8 URL available anymore, stream likely ended")
						return
					}
					if isCanceled(err, ctx) || ctx.Err() == context.Canceled {
						return
					}
					consecutivePlaylistFailures++
					logger.Warnf("hls: playlist fetch/parse failed (attempt=%d): %v", consecutivePlaylistFailures, err)

					// Retry once immediately to reduce the chance of missing short HLS windows.
					pl, err = fetchPlaylistWithRefresh()
					if err != nil {
						if errors.Is(err, ErrNoM3u8URL) {
							logger.Infof("hls: no m3u8 URL available anymore, stream likely ended")
							return
						}
						if isCanceled(err, ctx) || ctx.Err() == context.Canceled {
							return
						}
						consecutivePlaylistFailures++
						logger.Warnf("hls: immediate playlist retry failed (attempt=%d): %v", consecutivePlaylistFailures, err)
						if consecutivePlaylistFailures >= 3 {
							logger.Warnf("hls: consecutive playlist failures reached %d", consecutivePlaylistFailures)
							return
						}
						continue
					}

					logger.Warn("hls: playlist recovered by immediate retry")
				}

				if consecutivePlaylistFailures > 0 {
					logger.Infof("hls: playlist fetch/parse recovered after %d failure(s)", consecutivePlaylistFailures)
				}
				consecutivePlaylistFailures = 0

				updatedPollInterval := hlsutil.DerivePollInterval(pl)
				if updatedPollInterval != pollInterval {
					logger.Infof("hls: poll interval updated from %v to %v (target=%.2fs, first-extinf=%.2fs)", pollInterval, updatedPollInterval, pl.TargetDuration, func() float64 {
						if len(pl.Segments) > 0 {
							return pl.Segments[0].Duration
						}
						return 0
					}())
					pollInterval = updatedPollInterval
					ticker.Reset(pollInterval)
				}

				baseSeq, segs := pl.MediaSeq, pl.Segments
				pendingSegments := hlsutil.CountPendingSegments(baseSeq, segs, nextSeq)
				logger.Debugf("hls: playlist window base=%d len=%d next=%d pending=%d map=%t", baseSeq, len(segs), nextSeq, pendingSegments, pl.MapURI != "")

				if baseSeq > nextSeq {
					lost := baseSeq - nextSeq
					logger.Warnf("hls: sequence gap detected, likely missed %d segment(s) (nextSeq=%d, baseSeq=%d)", lost, nextSeq, baseSeq)
					nextSeq = baseSeq
				}
				if hlsutil.ShouldResetSequenceOnRollback(prevBaseSeq, baseSeq, len(segs), nextSeq) {
					logger.Warnf("hls: sequence rollback/discontinuity detected (nextSeq=%d, baseSeq=%d, window=%d), resetting nextSeq", nextSeq, baseSeq, len(segs))
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
					timer := time.NewTimer(waitForSync)
					select {
					case <-timer.C:
						lastSyncWaitBaseSeq = baseSeq
						logger.Debugf("hls: manifest sync wait completed base=%d", baseSeq)
					case <-ctx.Done():
						timer.Stop()
						return
					}
				}

				prefetcher := hlsutil.NewSegmentPrefetcher(ctx, segmentClient, resolver, segmentRetryAttempts, segmentRetryDelay)

				for i, seg := range segs {
					segSeq := baseSeq + int64(i)
					if segSeq < nextSeq {
						continue // already downloaded
					}

					if currentMapURI != "" && !mapSent {
						mapFetchStart := time.Now()
						mapURL, err := resolver.Resolve(currentMapURI)
						if err != nil {
							logger.Warnf("hls: cannot resolve map URL %q: %v", currentMapURI, err)
							continue
						}

						mapResp, err := segmentClient.R().SetContext(ctx).Get(mapURL)
						if err != nil {
							if isCanceled(err, ctx) {
								return
							}
							logger.Warnf("hls: map fetch error: %v", err)
							continue
						}
						if mapResp.StatusCode() != 200 {
							logger.Warnf("hls: map status %d", mapResp.StatusCode())
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

					for lookahead := 0; lookahead <= segmentPrefetchAhead; lookahead++ {
						nextIdx := i + lookahead
						if nextIdx >= len(segs) {
							break
						}
						nextPrefetchSeq := baseSeq + int64(nextIdx)
						if nextPrefetchSeq < nextSeq {
							continue
						}
						logger.Debugf("hls: prefetch start seq=%d uri=%s", nextPrefetchSeq, segs[nextIdx].URI)
						prefetcher.Start(nextPrefetchSeq, segs[nextIdx].URI)
					}

					waitStart := time.Now()
					data, err := prefetcher.Wait(segSeq, seg.URI)
					if err != nil {
						if isCanceled(err, ctx) || ctx.Err() == context.Canceled {
							return
						}
						logger.Warnf("hls: segment fetch error (seq=%d): %v", segSeq, err)
						continue
					}
					logger.Debugf("hls: segment ready seq=%d bytes=%d wait=%v", segSeq, len(data), time.Since(waitStart))

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
