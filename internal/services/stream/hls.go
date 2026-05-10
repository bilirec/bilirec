package stream

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

func isCanceled(err error, ctx context.Context) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.Canceled) || ctx.Err() == context.Canceled
}

// hlsSegment represents a single entry in an HLS playlist.
type hlsSegment struct {
	uri      string
	duration float64 // from #EXTINF
}

type hlsPlaylist struct {
	mediaSeq int64
	segments []hlsSegment
	mapURI   string // from #EXT-X-MAP:URI="..."
}

var (
	extXMediaSequencePrefix = []byte("#EXT-X-MEDIA-SEQUENCE:")
	extXMapPrefix           = []byte("#EXT-X-MAP:")
	extInfPrefix            = []byte("#EXTINF:")

	ErrM3u8Expired = errors.New("hls: m3u8 playlist expired")
	ErrNoM3u8URL   = errors.New("hls: no m3u8 url available")
)

func isM3u8URLExpiredStatus(status int) bool {
	switch status {
	case 401, 403, 404, 410:
		return true
	default:
		return false
	}
}

func isM3u8URLExpiredErr(err error) bool {
	return errors.Is(err, ErrM3u8Expired)
}

func parseAttributeURIBytes(line []byte) string {
	const key = "URI=\""
	idx := bytes.Index(line, []byte(key))
	if idx < 0 {
		return ""
	}
	start := idx + len(key)
	if start >= len(line) {
		return ""
	}
	endRel := bytes.IndexByte(line[start:], '"')
	if endRel < 0 {
		return ""
	}
	return string(line[start : start+endRel])
}

// parseM3u8 parses a media m3u8 playlist body and returns the media sequence
// base number and the list of segments in the playlist window.
func parseM3u8(body string) (playlist *hlsPlaylist, err error) {
	return parseM3u8Bytes([]byte(body))
}

func parseM3u8Bytes(body []byte) (playlist *hlsPlaylist, err error) {
	playlist = &hlsPlaylist{}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	var currentDuration float64
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := bytes.TrimSpace(scanner.Bytes())
		if bytes.HasPrefix(line, extXMediaSequencePrefix) {
			after := bytes.TrimSpace(line[len(extXMediaSequencePrefix):])
			seq, parseErr := strconv.ParseInt(string(after), 10, 64)
			if parseErr != nil {
				return playlist, fmt.Errorf("hls: invalid EXT-X-MEDIA-SEQUENCE on line %d: %w", lineNo, parseErr)
			}
			playlist.mediaSeq = seq
		} else if bytes.HasPrefix(line, extXMapPrefix) {
			afterMap := line[len(extXMapPrefix):]
			playlist.mapURI = parseAttributeURIBytes(afterMap)
		} else if bytes.HasPrefix(line, extInfPrefix) {
			// #EXTINF:<duration>[,<title>]
			raw := line[len(extInfPrefix):]
			if comma := bytes.IndexByte(raw, ','); comma >= 0 {
				raw = raw[:comma]
			}
			currentDuration, err = strconv.ParseFloat(string(bytes.TrimSpace(raw)), 64)
			if err != nil {
				return playlist, fmt.Errorf("hls: invalid EXTINF duration on line %d: %w", lineNo, err)
			}
		} else if len(line) > 0 && line[0] != '#' {
			playlist.segments = append(playlist.segments, hlsSegment{uri: string(line), duration: currentDuration})
			currentDuration = 0
		}
	}
	if err = scanner.Err(); err != nil {
		return nil, fmt.Errorf("hls: m3u8 scan error: %w", err)
	}
	return playlist, nil
}

// resolveSegmentURL resolves a (possibly relative) segment URI against the
// m3u8 playlist base URL.
func resolveSegmentURL(m3u8URL, segmentURI string) (string, error) {
	if strings.HasPrefix(segmentURI, "http://") || strings.HasPrefix(segmentURI, "https://") {
		return segmentURI, nil
	}
	base, err := url.Parse(m3u8URL)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(segmentURI)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}

type hlsURLResolver struct {
	base *url.URL
}

func newHlsURLResolver(m3u8URL string) (*hlsURLResolver, error) {
	base, err := url.Parse(m3u8URL)
	if err != nil {
		return nil, err
	}
	return &hlsURLResolver{base: base}, nil
}

func (r *hlsURLResolver) resolve(segmentURI string) (string, error) {
	if strings.HasPrefix(segmentURI, "http://") || strings.HasPrefix(segmentURI, "https://") {
		return segmentURI, nil
	}
	ref, err := url.Parse(segmentURI)
	if err != nil {
		return "", err
	}
	return r.base.ResolveReference(ref).String(), nil
}

func shouldResetSequenceOnRollback(prevBaseSeq int64, baseSeq int64, segCount int, nextSeq int64) bool {
	if segCount <= 0 {
		return false
	}
	if baseSeq >= nextSeq {
		return false
	}
	// If nextSeq still falls inside the current window, we have overlap and should keep going.
	windowEndExclusive := baseSeq + int64(segCount)
	if nextSeq < windowEndExclusive {
		return false
	}

	// Only treat as rollback when sequence numbers themselves moved backward.
	// If baseSeq keeps increasing (or stays the same), being at/after window tail is
	// normal live polling behavior (no new segment yet), not a discontinuity.
	return baseSeq < prevBaseSeq
}

// ReadHlsStream polls an HLS m3u8 playlist and delivers each new segment as a
// complete []byte to the returned channel. One send = one full TS or fMP4
// segment, ready to be written to disk.
//
// The polling interval is derived from the first #EXTINF duration seen
// (interval = duration/2), falling back to 1 second. This ensures each
// segment is fetched well before Bilibili prunes it from the playlist window.
func (r *Service) ReadHlsStream(fetchM3u8URL func() (string, error), client *resty.Client, ctx context.Context) (<-chan []byte, error) {
	var (
		m3u8URL       string
		resolver      *hlsURLResolver
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

		nextResolver, err := newHlsURLResolver(nextURL)
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
		resp, err := client.R().SetContext(ctx).Get(m3u8URL)
		if err != nil {
			return nil, fmt.Errorf("fetch m3u8: %w", err)
		}
		if isM3u8URLExpiredStatus(resp.StatusCode()) {
			return nil, fmt.Errorf("%w (status=%d)", ErrM3u8Expired, resp.StatusCode())
		} else if resp.StatusCode() != 200 {
			return nil, fmt.Errorf("m3u8 status %d", resp.StatusCode())
		}
		pl, err := parseM3u8Bytes(resp.Body())
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
	mediaSeq, segs := pl.mediaSeq, pl.segments

	// Derive poll interval from first segment duration.
	pollInterval := 1 * time.Second
	if len(segs) > 0 && segs[0].duration > 0 {
		pollInterval = time.Duration(segs[0].duration / 2 * float64(time.Second))
		if pollInterval < 500*time.Millisecond {
			pollInterval = 500 * time.Millisecond
		}
	}
	logger.Infof("hls: poll interval=%v (derived from EXTINF=%.2fs)", pollInterval, func() float64 {
		if len(segs) > 0 {
			return segs[0].duration
		}
		return 0
	}())

	// nextSeq is the sequence number of the next segment we want to fetch.
	// For fMP4 playlists (EXT-X-MAP present), start from the current window head
	// to improve startup decode stability (higher chance to include an IDR GOP).
	// For TS playlists, keep the old behavior and start from the window tail.
	nextSeq := mediaSeq + int64(len(segs))
	if pl.mapURI != "" {
		nextSeq = mediaSeq
	}
	currentMapURI = pl.mapURI
	mapSent = false

	ch := make(chan []byte, 5)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		consecutivePlaylistFailures := 0
		prevBaseSeq := mediaSeq

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

				baseSeq, segs := pl.mediaSeq, pl.segments
				if baseSeq > nextSeq {
					lost := baseSeq - nextSeq
					logger.Warnf("hls: sequence gap detected, likely missed %d segment(s) (nextSeq=%d, baseSeq=%d)", lost, nextSeq, baseSeq)
					nextSeq = baseSeq
				}
				if shouldResetSequenceOnRollback(prevBaseSeq, baseSeq, len(segs), nextSeq) {
					logger.Warnf("hls: sequence rollback/discontinuity detected (nextSeq=%d, baseSeq=%d, window=%d), resetting nextSeq", nextSeq, baseSeq, len(segs))
					nextSeq = baseSeq
					mapSent = false
				}

				if pl.mapURI != currentMapURI {
					currentMapURI = pl.mapURI
					mapSent = false
				}

				for i, seg := range segs {
					segSeq := baseSeq + int64(i)
					if segSeq < nextSeq {
						continue // already downloaded
					}

					if currentMapURI != "" && !mapSent {
						mapURL, err := resolver.resolve(currentMapURI)
						if err != nil {
							logger.Warnf("hls: cannot resolve map URL %q: %v", currentMapURI, err)
							continue
						}

						mapResp, err := client.R().SetContext(ctx).Get(mapURL)
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

						select {
						case ch <- mapResp.Body():
							mapSent = true
						case <-ctx.Done():
							return
						}
					}

					segURL, err := resolver.resolve(seg.uri)
					if err != nil {
						logger.Warnf("hls: cannot resolve segment URL %q: %v", seg.uri, err)
						continue
					}

					segResp, err := client.R().SetContext(ctx).Get(segURL)
					if err != nil {
						if isCanceled(err, ctx) {
							return
						}
						logger.Warnf("hls: segment fetch error (seq=%d): %v", segSeq, err)
						continue
					}
					if segResp.StatusCode() != 200 {
						logger.Warnf("hls: segment status %d (seq=%d)", segResp.StatusCode(), segSeq)
						continue
					}

					data := segResp.Body()
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
