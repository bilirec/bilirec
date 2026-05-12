package hls

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/go-resty/resty/v2"
	m3u8 "github.com/grafov/m3u8"
)

// Parse decodes a media playlist body.
func Parse(body string) (*Playlist, error) {
	return ParseBytes([]byte(body))
}

// ParseBytes decodes a media playlist body.
func ParseBytes(body []byte) (*Playlist, error) {
	playlist := &Playlist{}
	parsed, listType, err := m3u8.DecodeFrom(bytes.NewReader(body), true)
	if err != nil {
		return nil, fmt.Errorf("hls: m3u8 decode error: %w", err)
	}
	if listType != m3u8.MEDIA {
		return nil, fmt.Errorf("hls: expected media playlist, got %v", listType)
	}
	mediaPlaylist, ok := parsed.(*m3u8.MediaPlaylist)
	if !ok {
		return nil, fmt.Errorf("hls: unexpected playlist type %T", parsed)
	}

	playlist.MediaSeq = int64(mediaPlaylist.SeqNo)
	playlist.TargetDuration = mediaPlaylist.TargetDuration
	if mediaPlaylist.Map != nil {
		playlist.MapURI = mediaPlaylist.Map.URI
	}

	segments := mediaPlaylist.GetAllSegments()
	playlist.Segments = make([]Segment, 0, len(segments))
	for _, seg := range segments {
		if seg == nil {
			continue
		}
		playlist.Segments = append(playlist.Segments, Segment{URI: seg.URI, Duration: seg.Duration})
		if playlist.MapURI == "" && seg.Map != nil {
			playlist.MapURI = seg.Map.URI
		}
	}

	return playlist, nil
}

// IsM3u8URLExpiredStatus reports whether a playlist URL status implies expiry.
func IsM3u8URLExpiredStatus(status int) bool {
	switch status {
	case 401, 403, 404, 410:
		return true
	default:
		return false
	}
}

// ShouldResetSequenceOnRollback determines whether sequence should be reset.
func ShouldResetSequenceOnRollback(prevBaseSeq int64, baseSeq int64, segCount int, nextSeq int64) bool {
	if segCount <= 0 {
		return false
	}
	if baseSeq >= nextSeq {
		return false
	}
	windowEndExclusive := baseSeq + int64(segCount)
	if nextSeq < windowEndExclusive {
		return false
	}
	return baseSeq < prevBaseSeq
}

// DerivePollInterval calculates playlist poll interval from target/segment duration.
func DerivePollInterval(pl *Playlist) time.Duration {
	const minPollInterval = 500 * time.Millisecond
	const fallbackPollInterval = 1 * time.Second
	const pollRatio = 0.67

	if pl == nil {
		return fallbackPollInterval
	}

	baseDuration := pl.TargetDuration
	if baseDuration <= 0 && len(pl.Segments) > 0 {
		baseDuration = pl.Segments[0].Duration
	}
	if baseDuration <= 0 {
		return fallbackPollInterval
	}

	interval := time.Duration(math.Round(baseDuration * pollRatio * float64(time.Second)))
	if interval < minPollInterval {
		return minPollInterval
	}
	return interval
}

// DeriveManifestSyncWait calculates how long to wait for CDN sync after playlist refresh.
func DeriveManifestSyncWait(pl *Playlist, waitRate float64) time.Duration {
	const minSyncWait = 100 * time.Millisecond
	const maxSyncWait = 600 * time.Millisecond
	const fallbackSyncWait = 200 * time.Millisecond

	if pl == nil {
		return fallbackSyncWait
	}

	baseDuration := pl.TargetDuration
	if baseDuration <= 0 && len(pl.Segments) > 0 {
		baseDuration = pl.Segments[0].Duration
	}
	if baseDuration <= 0 {
		return fallbackSyncWait
	}

	wait := time.Duration(math.Round(baseDuration * waitRate * float64(time.Second)))
	if wait < minSyncWait {
		return minSyncWait
	}
	if wait > maxSyncWait {
		return maxSyncWait
	}
	return wait
}

// ShouldApplyManifestSyncWait prevents repeated wait on the same base sequence.
func ShouldApplyManifestSyncWait(baseSeq, nextSeq, lastWaitBaseSeq int64) bool {
	if nextSeq != baseSeq {
		return false
	}
	if baseSeq <= lastWaitBaseSeq {
		return false
	}
	return true
}

// CountPendingSegments returns number of not-yet-consumed segments in current window.
func CountPendingSegments(baseSeq int64, segs []Segment, nextSeq int64) int {
	pending := 0
	for i := range segs {
		if baseSeq+int64(i) >= nextSeq {
			pending++
		}
	}
	return pending
}

// IsRetryableSegmentStatus reports whether a segment HTTP status should be retried.
func IsRetryableSegmentStatus(status int) bool {
	switch status {
	case 404, 410:
		return true
	default:
		return false
	}
}

// FetchSegmentWithRetry fetches a segment with a fixed short retry policy.
func FetchSegmentWithRetry(ctx context.Context, client *resty.Client, segmentURL string, attempts int, delay time.Duration) ([]byte, error) {
	if attempts <= 0 {
		attempts = 1
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		resp, err := client.R().SetContext(ctx).Get(segmentURL)
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() == context.Canceled {
				return nil, err
			}
			return nil, fmt.Errorf("segment fetch: %w", err)
		}
		if resp.StatusCode() == 200 {
			return resp.Body(), nil
		}
		if !IsRetryableSegmentStatus(resp.StatusCode()) || attempt == attempts {
			return nil, fmt.Errorf("segment status %d", resp.StatusCode())
		}

		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}
	}

	return nil, fmt.Errorf("segment fetch retry exhausted")
}
