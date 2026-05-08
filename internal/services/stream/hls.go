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
func parseM3u8(body string) (playlist hlsPlaylist) {
	scanner := bufio.NewScanner(strings.NewReader(body))
	var currentDuration float64
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if after, ok := strings.CutPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"); ok {
			seq, _ := strconv.ParseInt(after, 10, 64)
			playlist.mediaSeq = seq
		} else if afterMap, okMap := strings.CutPrefix(line, "#EXT-X-MAP:"); okMap {
			playlist.mapURI = parseAttributeURI(afterMap)
		} else if after0, ok0 := strings.CutPrefix(line, "#EXTINF:"); ok0 {
			// #EXTINF:<duration>[,<title>]
			raw := after0
			raw = strings.Split(raw, ",")[0]
			currentDuration, _ = strconv.ParseFloat(raw, 64)
		} else if line != "" && !strings.HasPrefix(line, "#") {
			playlist.segments = append(playlist.segments, hlsSegment{uri: line, duration: currentDuration})
			currentDuration = 0
		}
	}
	return
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

// ReadHlsStream polls an HLS m3u8 playlist and delivers each new segment as a
// complete []byte to the returned channel. One send = one full TS or fMP4
// segment, ready to be written to disk.
//
// The polling interval is derived from the first #EXTINF duration seen
// (interval = duration/2), falling back to 1 second. This ensures each
// segment is fetched well before Bilibili prunes it from the playlist window.
func (r *Service) ReadHlsStream(m3u8URL string, ctx context.Context) (<-chan []byte, error) {
	client := resty.New()

	// Fetch the initial playlist to verify reachability and derive poll interval.
	resp, err := client.R().SetContext(ctx).Get(m3u8URL)
	if err != nil {
		return nil, fmt.Errorf("hls: cannot fetch initial m3u8: %w", err)
	}
	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("hls: m3u8 returned status %d", resp.StatusCode())
	}

	pl := parseM3u8(string(resp.Body()))
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

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				resp, err := client.R().SetContext(ctx).Get(m3u8URL)
				if err != nil {
					if isCanceled(err, ctx) {
						return
					}
					logger.Warnf("hls: m3u8 fetch error: %v", err)
					continue
				}
				if resp.StatusCode() != 200 {
					logger.Warnf("hls: m3u8 status %d", resp.StatusCode())
					continue
				}

				pl := parseM3u8(string(resp.Body()))
				baseSeq, segs := pl.mediaSeq, pl.segments

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
			}
		}
	}()

	return ch, nil
}
