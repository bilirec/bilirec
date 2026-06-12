package stream

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/benchreport"
	"github.com/go-resty/resty/v2"
)

func benchmarkPlaylistBody(mediaSeq int64, segURI string) string {
	return strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-VERSION:7",
		"#EXT-X-TARGETDURATION:1",
		fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d", mediaSeq),
		"#EXT-X-MAP:URI=\"init.mp4\"",
		"#EXTINF:1.0,",
		segURI,
	}, "\n")
}

func BenchmarkReadHlsStream_DeliveryLatency(b *testing.B) {
	var mediaSeq int64 = 1000
	playlistPath := "/playlist.m3u8"
	segmentPath := "/seg.ts"
	mapPath := "/init.mp4"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case playlistPath:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(benchmarkPlaylistBody(mediaSeq, segmentPath)))
			mediaSeq++
		case segmentPath:
			time.Sleep(2 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(make([]byte, 256*1024))
		case mapPath:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(make([]byte, 1024))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	svc := &Service{chanBufferSize: 64}
	playlistClient := resty.New().SetTimeout(3 * time.Second)
	segmentClient := resty.New().SetTimeout(5 * time.Second)
	fetchURL := func() (string, error) { return server.URL + playlistPath, nil }

	// map (~1KB) + first segment (~256KB) per iteration
	mon := benchreport.Start(b, 257*1024)
	b.ReportAllocs()
	b.ResetTimer()
	mon.MarkTimerStart()

	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		start := time.Now()
		ch, err := svc.ReadHlsStream(fetchURL, playlistClient, segmentClient, ctx, 10000)
		if err != nil {
			cancel()
			b.Fatalf("ReadHlsStream failed: %v", err)
		}

		got := 0
		timeout := time.NewTimer(2 * time.Second)
	loop:
		for got < 2 { // init map + first media segment
			select {
			case _, ok := <-ch:
				if !ok {
					break loop
				}
				got++
			case <-timeout.C:
				cancel()
				b.Fatalf("timeout waiting for hls data")
			}
		}
		timeout.Stop()
		cancel()
		b.ReportMetric(float64(time.Since(start).Microseconds()), "fetch_to_deliver_us")
		mon.SamplePeriodically(i)
	}
}
