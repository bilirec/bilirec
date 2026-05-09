package stream

import (
	"bufio"
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

func parseAttributeURI(line string) string {
	const key = "URI=\""
	idx := strings.Index(line, key)
	if idx < 0 {
		return ""
	}
	start := idx + len(key)
	if start >= len(line) {
		return ""
	}
	endRel := strings.IndexByte(line[start:], '"')
	if endRel < 0 {
		return ""
	}
	return line[start : start+endRel]
}

// parseM3u8 parses a media m3u8 playlist body and returns the media sequence
// base number and the list of segments in the playlist window.
func parseM3u8(body string) (playlist *hlsPlaylist, err error) {
	playlist = &hlsPlaylist{}
	scanner := bufio.NewScanner(strings.NewReader(body))
	var currentDuration float64
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if after, ok := strings.CutPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"); ok {
			seq, parseErr := strconv.ParseInt(strings.TrimSpace(after), 10, 64)
			if parseErr != nil {
				return playlist, fmt.Errorf("hls: invalid EXT-X-MEDIA-SEQUENCE on line %d: %w", lineNo, parseErr)
			}
			playlist.mediaSeq = seq
		} else if afterMap, okMap := strings.CutPrefix(line, "#EXT-X-MAP:"); okMap {
			playlist.mapURI = parseAttributeURI(afterMap)
		} else if after0, ok0 := strings.CutPrefix(line, "#EXTINF:"); ok0 {
			// #EXTINF:<duration>[,<title>]
			raw := after0
			raw = strings.Split(raw, ",")[0]
			currentDuration, err = strconv.ParseFloat(strings.TrimSpace(raw), 64)
			if err != nil {
				return playlist, fmt.Errorf("hls: invalid EXTINF duration on line %d: %w", lineNo, err)
			}
		} else if line != "" && !strings.HasPrefix(line, "#") {
			playlist.segments = append(playlist.segments, hlsSegment{uri: line, duration: currentDuration})
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
func (r *Service) ReadHlsStream(m3u8URL string, client *resty.Client, ctx context.Context) (<-chan []byte, error) {
	fetchPlaylist := func() (*hlsPlaylist, error) {
		resp, err := client.R().SetContext(ctx).Get(m3u8URL)
		if err != nil {
			return nil, fmt.Errorf("fetch m3u8: %w", err)
		}
		if resp.StatusCode() != 200 {
			return nil, fmt.Errorf("m3u8 status %d", resp.StatusCode())
		}
		pl, err := parseM3u8(string(resp.Body()))
		if err != nil {
			return nil, fmt.Errorf("parse m3u8: %w", err)
		}
		return pl, nil
	}

	// Fetch the initial playlist to verify reachability and derive poll interval.
	pl, err := fetchPlaylist()
	if err != nil {
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
	currentMapURI := pl.mapURI
	mapSent := false

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
				pl, err := fetchPlaylist()
				if err != nil {
					if isCanceled(err, ctx) || ctx.Err() == context.Canceled {
						return
					}
					consecutivePlaylistFailures++
					logger.Warnf("hls: playlist fetch/parse failed (attempt=%d): %v", consecutivePlaylistFailures, err)

					// Retry once immediately to reduce the chance of missing short HLS windows.
					pl, err = fetchPlaylist()
					if err != nil {
						if isCanceled(err, ctx) || ctx.Err() == context.Canceled {
							return
						}
						consecutivePlaylistFailures++
						logger.Warnf("hls: immediate playlist retry failed (attempt=%d): %v", consecutivePlaylistFailures, err)
						if consecutivePlaylistFailures >= 3 {
							logger.Warnf("hls: consecutive playlist failures reached %d", consecutivePlaylistFailures)
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
						mapURL, err := resolveSegmentURL(m3u8URL, currentMapURI)
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

					segURL, err := resolveSegmentURL(m3u8URL, seg.uri)
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
