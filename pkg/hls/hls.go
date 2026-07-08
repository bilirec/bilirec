package hls

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strings"
	"time"

	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/go-resty/resty/v2"
	m3u8 "github.com/grafov/m3u8"
)

const (
	segmentScratchSize    = 32 * 1024
	segmentBodyInitialCap = 256 * 1024
	segmentBodyMaxCap     = 4 * 1024 * 1024
)

var (
	segmentBodyPool    = pool.NewBufferPool(segmentBodyInitialCap, segmentBodyMaxCap)
	segmentScratchPool = pool.NewBytesPool(segmentScratchSize)
)

// SegmentBodyReader reads an HTTP segment response body into a reusable buffer.
type SegmentBodyReader func(resp *resty.Response) ([]byte, error)

// SegmentScratchBytes returns a scratch buffer for segment body reads.
func SegmentScratchBytes() []byte {
	return segmentScratchPool.GetBytes()
}

// ReleaseSegmentScratch returns a scratch buffer to the pool.
func ReleaseSegmentScratch(scratch []byte) {
	segmentScratchPool.PutBytes(scratch)
}

// Parse decodes a media playlist body.
func Parse(body string) (*Playlist, error) {
	return ParseBytes([]byte(body))
}

// ParseBytes decodes a media playlist body.
func ParseBytes(body []byte) (*Playlist, error) {
	playlist := &Playlist{}
	parsed, listType, err := m3u8.DecodeFrom(bytes.NewReader(body), true)
	if err != nil {
		return nil, fmt.Errorf("hls：m3u8 解码错误：%w", err)
	}
	if listType != m3u8.MEDIA {
		return nil, fmt.Errorf("hls：期望 media playlist，实际为 %v", listType)
	}
	mediaPlaylist, ok := parsed.(*m3u8.MediaPlaylist)
	if !ok {
		return nil, fmt.Errorf("hls：意外的播放列表类型 %T", parsed)
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

// IsRetryableFetchErr reports whether a transient HTTP fetch error should be retried.
func IsRetryableFetchErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}

	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "timeout") ||
		strings.Contains(errText, "deadline exceeded") ||
		strings.Contains(errText, "connection reset") ||
		strings.Contains(errText, "broken pipe") ||
		strings.Contains(errText, "goaway") ||
		strings.Contains(errText, "eof")
}

// IsRetryableSegmentFetchErr is an alias kept for call-site clarity.
func IsRetryableSegmentFetchErr(err error) bool {
	return IsRetryableFetchErr(err)
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

func closeSegmentResponseBody(resp *resty.Response) {
	if resp == nil {
		return
	}
	body := resp.RawBody()
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}

func readSegmentBody(resp *resty.Response) ([]byte, error) {
	body := resp.RawBody()
	if body == nil {
		return nil, fmt.Errorf("拉取分片失败：响应体为空")
	}
	defer body.Close()

	buf := segmentBodyPool.Get()
	defer segmentBodyPool.Put(buf)
	buf.Reset()

	if resp.RawResponse != nil && resp.RawResponse.ContentLength > 0 {
		buf.Grow(int(resp.RawResponse.ContentLength))
	}

	scratch := segmentScratchPool.GetBytes()
	defer segmentScratchPool.PutBytes(scratch)

	if _, err := io.CopyBuffer(buf, body, scratch); err != nil {
		return nil, fmt.Errorf("读取分片失败：%w", err)
	}

	out := make([]byte, buf.Len())
	copy(out, buf.Bytes())
	return out, nil
}

func waitBeforeSegmentRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	}
}

// FetchSegmentWithRetry fetches a segment with a fixed short retry policy.
func FetchSegmentWithRetry(ctx context.Context, client *resty.Client, segmentURL string, attempts int, delay time.Duration) ([]byte, error) {
	return FetchSegmentWithRetryReader(ctx, client, segmentURL, attempts, delay, readSegmentBody)
}

// FetchSegmentWithRetryReader fetches a segment using readBody to load the response.
func FetchSegmentWithRetryReader(
	ctx context.Context,
	client *resty.Client,
	segmentURL string,
	attempts int,
	delay time.Duration,
	readBody SegmentBodyReader,
) ([]byte, error) {
	if readBody == nil {
		readBody = readSegmentBody
	}
	if attempts <= 0 {
		attempts = 1
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		resp, err := client.R().
			SetContext(ctx).
			SetDoNotParseResponse(true).
			Get(segmentURL)
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() == context.Canceled {
				return nil, err
			}
			if attempt < attempts && IsRetryableFetchErr(err) {
				if waitErr := waitBeforeSegmentRetry(ctx, delay); waitErr != nil {
					return nil, waitErr
				}
				continue
			}
			return nil, fmt.Errorf("拉取分片失败：%w", err)
		}

		if resp.StatusCode() == 200 {
			data, readErr := readBody(resp)
			if readErr == nil {
				return data, nil
			}
			if errors.Is(readErr, context.Canceled) || ctx.Err() == context.Canceled {
				return nil, readErr
			}
			if attempt < attempts && IsRetryableFetchErr(readErr) {
				if waitErr := waitBeforeSegmentRetry(ctx, delay); waitErr != nil {
					return nil, waitErr
				}
				continue
			}
			return nil, readErr
		}

		status := resp.StatusCode()
		closeSegmentResponseBody(resp)

		if !IsRetryableSegmentStatus(status) || attempt == attempts {
			return nil, fmt.Errorf("分片状态码 %d", status)
		}

		if waitErr := waitBeforeSegmentRetry(ctx, delay); waitErr != nil {
			return nil, waitErr
		}
	}

	return nil, fmt.Errorf("分片拉取重试次数已耗尽")
}
