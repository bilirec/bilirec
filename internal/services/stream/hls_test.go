package stream

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/benchreport"
	hlsutil "github.com/bilirec/bilirec/pkg/hls"
	"github.com/go-resty/resty/v2"
)

type hlsSegment = hlsutil.Segment
type hlsPlaylist = hlsutil.Playlist

func buildPlaylistBody(segCount int, withMap bool) string {
	lines := make([]string, 0, segCount*2+4)
	lines = append(lines,
		"#EXTM3U",
		"#EXT-X-VERSION:3",
		"#EXT-X-MEDIA-SEQUENCE:1000",
	)
	if withMap {
		lines = append(lines, "#EXT-X-MAP:URI=\"init.mp4\"")
	}
	for i := 0; i < segCount; i++ {
		lines = append(lines,
			"#EXTINF:2.0,",
			fmt.Sprintf("seg-%d.ts", 1000+i),
		)
	}
	return strings.Join(lines, "\n")
}

func TestParseM3u8_ValidPlaylist(t *testing.T) {
	body := strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-VERSION:3",
		"#EXT-X-TARGETDURATION:8",
		"#EXT-X-MEDIA-SEQUENCE:123",
		"#EXT-X-MAP:URI=\"init.mp4\"",
		"#EXTINF:2.0,",
		"seg-123.ts",
		"#EXTINF:2.5,",
		"seg-124.ts",
	}, "\n")

	pl, err := hlsutil.Parse(body)
	if err != nil {
		t.Fatalf("parseM3u8 returned error: %v", err)
	}
	if pl.MediaSeq != 123 {
		t.Fatalf("unexpected mediaSeq: got=%d want=123", pl.MediaSeq)
	}
	if pl.TargetDuration != 8 {
		t.Fatalf("unexpected targetDuration: got=%v want=8", pl.TargetDuration)
	}
	if len(pl.Segments) != 2 {
		t.Fatalf("unexpected segment count: got=%d want=2", len(pl.Segments))
	}
	if pl.MapURI != "init.mp4" {
		t.Fatalf("unexpected map URI: got=%q want=init.mp4", pl.MapURI)
	}
	if pl.Segments[0].URI != "seg-123.ts" || pl.Segments[0].Duration != 2.0 {
		t.Fatalf("unexpected first segment: %+v", pl.Segments[0])
	}
	if pl.Segments[1].URI != "seg-124.ts" || pl.Segments[1].Duration != 2.5 {
		t.Fatalf("unexpected second segment: %+v", pl.Segments[1])
	}
}

func TestParseM3u8_InvalidMediaSequence(t *testing.T) {
	body := strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-MEDIA-SEQUENCE:not-a-number",
		"#EXTINF:2.0,",
		"seg.ts",
	}, "\n")

	_, err := hlsutil.Parse(body)
	if err == nil {
		t.Fatal("expected parseM3u8 to return error for invalid media sequence")
	}
}

func TestParseM3u8_InvalidExtinfDuration(t *testing.T) {
	body := strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-MEDIA-SEQUENCE:1",
		"#EXTINF:not-a-float,",
		"seg.ts",
	}, "\n")

	_, err := hlsutil.Parse(body)
	if err == nil {
		t.Fatal("expected parseM3u8 to return error for invalid EXTINF duration")
	}
}

func TestParseM3u8Bytes_UsesGrafovM3u8(t *testing.T) {
	body := []byte(strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-VERSION:3",
		"#EXT-X-TARGETDURATION:10",
		"#EXT-X-MEDIA-SEQUENCE:1000",
		"#EXT-X-MAP:URI=\"init.mp4\"",
		"#EXTINF:4.2,",
		"seg-1000.ts",
		"#EXTINF:4.1,",
		"seg-1001.ts",
	}, "\n"))

	pl, err := hlsutil.ParseBytes(body)
	if err != nil {
		t.Fatalf("parseM3u8Bytes returned error: %v", err)
	}
	if pl.TargetDuration != 10 {
		t.Fatalf("unexpected targetDuration: got=%v want=10", pl.TargetDuration)
	}
	if pl.MediaSeq != 1000 {
		t.Fatalf("unexpected mediaSeq: got=%d want=1000", pl.MediaSeq)
	}
	if pl.MapURI != "init.mp4" {
		t.Fatalf("unexpected map URI: got=%q want=init.mp4", pl.MapURI)
	}
	if got := len(pl.Segments); got != 2 {
		t.Fatalf("unexpected segment count: got=%d want=2", got)
	}
}

func TestDerivePollInterval(t *testing.T) {
	tests := []struct {
		name string
		pl   *hlsPlaylist
		want time.Duration
	}{
		{name: "target duration wins", pl: &hlsPlaylist{TargetDuration: 10, Segments: []hlsSegment{{Duration: 2}}}, want: 6700 * time.Millisecond},
		{name: "fallback to first extinf", pl: &hlsPlaylist{Segments: []hlsSegment{{Duration: 3}}}, want: 2010 * time.Millisecond},
		{name: "fallback to one second", pl: &hlsPlaylist{}, want: time.Second},
		{name: "minimum floor", pl: &hlsPlaylist{TargetDuration: 0.1}, want: 500 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hlsutil.DerivePollInterval(tt.pl)
			if got != tt.want {
				t.Fatalf("derivePollInterval()=%v want=%v", got, tt.want)
			}
		})
	}
}

func TestDeriveManifestSyncWait(t *testing.T) {
	tests := []struct {
		name string
		pl   *hlsPlaylist
		want time.Duration
	}{
		{name: "target duration based", pl: &hlsPlaylist{TargetDuration: 10}, want: 600 * time.Millisecond},
		{name: "fallback first extinf", pl: &hlsPlaylist{Segments: []hlsSegment{{Duration: 2}}}, want: 200 * time.Millisecond},
		{name: "minimum floor", pl: &hlsPlaylist{TargetDuration: 0.2}, want: 100 * time.Millisecond},
		{name: "fallback default", pl: &hlsPlaylist{}, want: 200 * time.Millisecond},
		{name: "maximum cap", pl: &hlsPlaylist{TargetDuration: 20}, want: 600 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hlsutil.DeriveManifestSyncWait(tt.pl, hlsutil.ManifestSyncWaitRate)
			if got != tt.want {
				t.Fatalf("deriveManifestSyncWait()=%v want=%v", got, tt.want)
			}
		})
	}
}

func TestShouldApplyManifestSyncWait(t *testing.T) {
	tests := []struct {
		name            string
		baseSeq         int64
		nextSeq         int64
		lastWaitBaseSeq int64
		want            bool
	}{
		{name: "at window head and not waited yet", baseSeq: 100, nextSeq: 100, lastWaitBaseSeq: 99, want: true},
		{name: "already waited for this base", baseSeq: 100, nextSeq: 100, lastWaitBaseSeq: 100, want: false},
		{name: "backlog catch up should not wait", baseSeq: 120, nextSeq: 110, lastWaitBaseSeq: 119, want: false},
		{name: "ahead of head should not wait", baseSeq: 100, nextSeq: 101, lastWaitBaseSeq: 99, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hlsutil.ShouldApplyManifestSyncWait(tt.baseSeq, tt.nextSeq, tt.lastWaitBaseSeq)
			if got != tt.want {
				t.Fatalf("shouldApplyManifestSyncWait(%d, %d, %d)=%v want=%v", tt.baseSeq, tt.nextSeq, tt.lastWaitBaseSeq, got, tt.want)
			}
		})
	}
}

func TestFetchSegmentWithShortRetry(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) < 3 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("segment-bytes"))
	}))
	defer server.Close()

	oldDelay := hlsutil.SegmentRetryDelay
	hlsutil.SegmentRetryDelay = 10 * time.Millisecond
	t.Cleanup(func() { hlsutil.SegmentRetryDelay = oldDelay })

	client := resty.New()
	ctx := t.Context()
	data, err := hlsutil.FetchSegmentWithRetry(ctx, client, server.URL, hlsutil.SegmentRetryAttempts, hlsutil.SegmentRetryDelay)
	if err != nil {
		t.Fatalf("fetchSegmentWithShortRetry returned error: %v", err)
	}
	if string(data) != "segment-bytes" {
		t.Fatalf("unexpected data: %q", string(data))
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("unexpected attempt count: got=%d want=3", got)
	}
}

func TestParseM3u8_ScannerErrorOnLongLine(t *testing.T) {
	// bufio.Scanner has a default token limit of 64K.
	veryLongLine := strings.Repeat("a", 70*1024)
	body := strings.Join([]string{
		"#EXTM3U",
		veryLongLine,
	}, "\n")

	_, err := hlsutil.Parse(body)
	if err == nil {
		t.Fatal("expected parseM3u8 to return scanner error for oversized line")
	}
}

func TestShouldResetSequenceOnRollback(t *testing.T) {
	tests := []struct {
		name     string
		prevBase int64
		baseSeq  int64
		segCount int
		nextSeq  int64
		want     bool
	}{
		{
			name:     "no segments no reset",
			prevBase: 100,
			baseSeq:  100,
			segCount: 0,
			nextSeq:  120,
			want:     false,
		},
		{
			name:     "base ahead of next no rollback",
			prevBase: 90,
			baseSeq:  120,
			segCount: 4,
			nextSeq:  100,
			want:     false,
		},
		{
			name:     "rollback with overlap should not reset",
			prevBase: 110,
			baseSeq:  95,
			segCount: 10,
			nextSeq:  100,
			want:     false,
		},
		{
			name:     "rollback with full window behind should reset",
			prevBase: 120,
			baseSeq:  95,
			segCount: 5,
			nextSeq:  100,
			want:     true,
		},
		{
			name:     "live edge boundary without rollback should not reset",
			prevBase: 199,
			baseSeq:  200,
			segCount: 3,
			nextSeq:  203,
			want:     false,
		},
		{
			name:     "window fully behind but base still advancing should not reset",
			prevBase: 7,
			baseSeq:  8,
			segCount: 3,
			nextSeq:  11,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hlsutil.ShouldResetSequenceOnRollback(tt.prevBase, tt.baseSeq, tt.segCount, tt.nextSeq)
			if got != tt.want {
				t.Fatalf("shouldResetSequenceOnRollback(%d, %d, %d, %d)=%v want=%v", tt.prevBase, tt.baseSeq, tt.segCount, tt.nextSeq, got, tt.want)
			}
		})
	}
}

func makeFtypInit(brand string) []byte {
	init := make([]byte, 20)
	init[0], init[1], init[2], init[3] = 0x00, 0x00, 0x00, 0x14
	copy(init[4:8], "ftyp")
	copy(init[8:12], []byte(brand))
	copy(init[16:20], []byte(brand))
	return init
}

func makeMoofSeg() []byte {
	moof := make([]byte, 16)
	moof[0], moof[1], moof[2], moof[3] = 0x00, 0x00, 0x00, 0x10
	copy(moof[4:8], "moof")
	return moof
}

func boxTypeOf(data []byte) string {
	if len(data) < 8 {
		return ""
	}
	return string(data[4:8])
}

func TestReadHlsStream_MapURIChangeSameInitContinues(t *testing.T) {
	initStreamTestConfig(t)
	oldPoll := hlsutil.PlaylistRetryDelay
	oldDebounce := hlsutil.Fmp4InitDebounceWindow
	hlsutil.PlaylistRetryDelay = 5 * time.Millisecond
	hlsutil.Fmp4InitDebounceWindow = 50 * time.Millisecond
	t.Cleanup(func() {
		hlsutil.PlaylistRetryDelay = oldPoll
		hlsutil.Fmp4InitDebounceWindow = oldDebounce
	})

	var mediaSeq atomic.Int64
	mediaSeq.Store(1000)
	var mapName atomic.Value
	mapName.Store("init-a.mp4")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/playlist.m3u8":
			body := strings.Join([]string{
				"#EXTM3U",
				"#EXT-X-VERSION:7",
				"#EXT-X-TARGETDURATION:1",
				fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d", mediaSeq.Load()),
				fmt.Sprintf("#EXT-X-MAP:URI=%q", mapName.Load().(string)),
				"#EXTINF:1.0,",
				fmt.Sprintf("seg-%d.m4s", mediaSeq.Load()),
			}, "\n")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(body))
		case strings.HasPrefix(r.URL.Path, "/init-"):
			// Same init bytes for both URIs (URI-only change).
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(makeFtypInit("isom"))
		case strings.HasPrefix(r.URL.Path, "/seg-"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(makeMoofSeg())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	svc := &Service{
		chunkPools:     newTestChunkPools(64*1024, 128*1024),
		chanBufferSize: 16,
	}
	playlistClient := resty.New().SetTimeout(3 * time.Second)
	segmentClient := resty.New().SetTimeout(5 * time.Second)
	fetchURL := func() (string, error) { return server.URL + "/playlist.m3u8", nil }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	chunkPool, releasePool := svc.AcquireChunkPool(10000)
	ch, err := svc.ReadHlsStream(fetchURL, playlistClient, segmentClient, ctx, 10000, chunkPool, releasePool)
	if err != nil {
		t.Fatalf("ReadHlsStream: %v", err)
	}

	ftypCount := 0
	moofCount := 0
	mapSwitched := false
	deadline := time.After(8 * time.Second)
	// Live playlist only exposes one segment; keep advancing mediaSeq so the
	// stream can continue after the URI-only map change.
	for moofCount < 3 {
		select {
		case data, ok := <-ch:
			if !ok {
				t.Fatal("channel closed early")
			}
			switch boxTypeOf(data) {
			case "ftyp":
				ftypCount++
			case "moof":
				moofCount++
				if !mapSwitched {
					mapName.Store("init-a-cdn.mp4")
					mapSwitched = true
				}
				mediaSeq.Add(1)
			}
		case <-deadline:
			t.Fatalf("timeout (ftyp=%d moof=%d mapSwitched=%v)", ftypCount, moofCount, mapSwitched)
		}
	}
	if !mapSwitched {
		t.Fatal("expected map URI to switch during the test")
	}
	if ftypCount != 1 {
		t.Fatalf("URI-only map change must not re-deliver init, ftypCount=%d", ftypCount)
	}
}

func TestReadHlsStream_ChangedInitSettlesAfterDebounce(t *testing.T) {
	initStreamTestConfig(t)
	oldPoll := hlsutil.PlaylistRetryDelay
	oldDebounce := hlsutil.Fmp4InitDebounceWindow
	hlsutil.PlaylistRetryDelay = 5 * time.Millisecond
	hlsutil.Fmp4InitDebounceWindow = 80 * time.Millisecond
	t.Cleanup(func() {
		hlsutil.PlaylistRetryDelay = oldPoll
		hlsutil.Fmp4InitDebounceWindow = oldDebounce
	})

	var mediaSeq atomic.Int64
	mediaSeq.Store(1000)
	var mapName atomic.Value
	mapName.Store("init-a.mp4")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/playlist.m3u8":
			body := strings.Join([]string{
				"#EXTM3U",
				"#EXT-X-VERSION:7",
				"#EXT-X-TARGETDURATION:1",
				fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d", mediaSeq.Load()),
				fmt.Sprintf("#EXT-X-MAP:URI=%q", mapName.Load().(string)),
				"#EXTINF:1.0,",
				fmt.Sprintf("seg-%d.m4s", mediaSeq.Load()),
			}, "\n")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(body))
		case strings.HasPrefix(r.URL.Path, "/init-"):
			brand := "isom"
			if strings.Contains(r.URL.Path, "init-b") {
				brand = "mp42"
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(makeFtypInit(brand))
		case strings.HasPrefix(r.URL.Path, "/seg-"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(makeMoofSeg())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	svc := &Service{
		chunkPools:     newTestChunkPools(64*1024, 128*1024),
		chanBufferSize: 16,
	}
	playlistClient := resty.New().SetTimeout(3 * time.Second)
	segmentClient := resty.New().SetTimeout(5 * time.Second)
	fetchURL := func() (string, error) { return server.URL + "/playlist.m3u8", nil }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	chunkPool, releasePool := svc.AcquireChunkPool(10000)
	ch, err := svc.ReadHlsStream(fetchURL, playlistClient, segmentClient, ctx, 10000, chunkPool, releasePool)
	if err != nil {
		t.Fatalf("ReadHlsStream: %v", err)
	}

	var brands []string
	sawSecondInit := false
	deadline := time.After(8 * time.Second)
	for !sawSecondInit {
		select {
		case data, ok := <-ch:
			if !ok {
				t.Fatal("channel closed before settled init")
			}
			if boxTypeOf(data) == "ftyp" {
				brand := string(data[8:12])
				brands = append(brands, brand)
				if brand == "isom" && mapName.Load().(string) == "init-a.mp4" {
					mapName.Store("init-b.mp4")
					mediaSeq.Add(1)
				}
				if brand == "mp42" {
					sawSecondInit = true
				}
			}
			if boxTypeOf(data) == "moof" && mapName.Load().(string) == "init-a.mp4" {
				mapName.Store("init-b.mp4")
				mediaSeq.Add(1)
			}
		case <-deadline:
			t.Fatalf("timeout waiting for settled mp42 init, brands=%v", brands)
		}
	}
	if len(brands) < 2 || brands[0] != "isom" || brands[len(brands)-1] != "mp42" {
		t.Fatalf("expected isom then settled mp42, got %v", brands)
	}
}

func TestReadHlsStream_InitChurnBackToConfirmedCancels(t *testing.T) {
	initStreamTestConfig(t)
	oldPoll := hlsutil.PlaylistRetryDelay
	oldDebounce := hlsutil.Fmp4InitDebounceWindow
	hlsutil.PlaylistRetryDelay = 5 * time.Millisecond
	// Long window so A→B→A happens before settle.
	hlsutil.Fmp4InitDebounceWindow = 2 * time.Second
	t.Cleanup(func() {
		hlsutil.PlaylistRetryDelay = oldPoll
		hlsutil.Fmp4InitDebounceWindow = oldDebounce
	})

	var mediaSeq atomic.Int64
	mediaSeq.Store(1000)
	var mapName atomic.Value
	mapName.Store("init-a.mp4")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/playlist.m3u8":
			body := strings.Join([]string{
				"#EXTM3U",
				"#EXT-X-VERSION:7",
				"#EXT-X-TARGETDURATION:1",
				fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d", mediaSeq.Load()),
				fmt.Sprintf("#EXT-X-MAP:URI=%q", mapName.Load().(string)),
				"#EXTINF:1.0,",
				fmt.Sprintf("seg-%d.m4s", mediaSeq.Load()),
			}, "\n")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(body))
		case strings.HasPrefix(r.URL.Path, "/init-"):
			brand := "isom"
			if strings.Contains(r.URL.Path, "init-b") {
				brand = "mp42"
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(makeFtypInit(brand))
		case strings.HasPrefix(r.URL.Path, "/seg-"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(makeMoofSeg())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	svc := &Service{
		chunkPools:     newTestChunkPools(64*1024, 128*1024),
		chanBufferSize: 16,
	}
	playlistClient := resty.New().SetTimeout(3 * time.Second)
	segmentClient := resty.New().SetTimeout(5 * time.Second)
	fetchURL := func() (string, error) { return server.URL + "/playlist.m3u8", nil }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	chunkPool, releasePool := svc.AcquireChunkPool(10000)
	ch, err := svc.ReadHlsStream(fetchURL, playlistClient, segmentClient, ctx, 10000, chunkPool, releasePool)
	if err != nil {
		t.Fatalf("ReadHlsStream: %v", err)
	}

	phase := 0 // 0: wait first media, 1: switched to B, 2: switched back to A
	ftypBrands := []string{}
	moofAfterReturn := 0
	deadline := time.After(8 * time.Second)
	// While B is pending, media is buffered (not on ch). Drive A→B→A with a
	// timer: wait past one poll so B enters debounce, then return to A before
	// the 2s settle window ends.
	var churnBack <-chan time.Time
	for moofAfterReturn < 2 {
		select {
		case data, ok := <-ch:
			if !ok {
				t.Fatal("channel closed early")
			}
			switch boxTypeOf(data) {
			case "ftyp":
				brand := string(data[8:12])
				ftypBrands = append(ftypBrands, brand)
				if brand == "mp42" {
					t.Fatal("churn back to A must not settle/deliver B init")
				}
			case "moof":
				switch phase {
				case 0:
					mapName.Store("init-b.mp4")
					mediaSeq.Add(1)
					phase = 1
					churnBack = time.After(900 * time.Millisecond)
				case 2:
					moofAfterReturn++
					mediaSeq.Add(1)
				}
			}
		case <-churnBack:
			if phase == 1 {
				mapName.Store("init-a.mp4")
				mediaSeq.Add(1)
				phase = 2
			}
			churnBack = nil
		case <-deadline:
			t.Fatalf("timeout phase=%d brands=%v moofAfterReturn=%d", phase, ftypBrands, moofAfterReturn)
		}
	}
	if len(ftypBrands) != 1 || ftypBrands[0] != "isom" {
		t.Fatalf("expected only initial isom init, got %v", ftypBrands)
	}
}

func TestReadHlsStream_MapFetchFailureDoesNotSkipSeq(t *testing.T) {
	initStreamTestConfig(t)
	oldPoll := hlsutil.PlaylistRetryDelay
	hlsutil.PlaylistRetryDelay = 5 * time.Millisecond
	t.Cleanup(func() { hlsutil.PlaylistRetryDelay = oldPoll })

	var mediaSeq atomic.Int64
	mediaSeq.Store(3000)
	var mapFailOnce atomic.Bool
	mapFailOnce.Store(true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/playlist.m3u8":
			seq := mediaSeq.Load()
			body := strings.Join([]string{
				"#EXTM3U",
				"#EXT-X-VERSION:7",
				"#EXT-X-TARGETDURATION:1",
				fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d", seq),
				`#EXT-X-MAP:URI="init.mp4"`,
				"#EXTINF:1.0,",
				fmt.Sprintf("seg-%d.m4s", seq),
			}, "\n")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(body))
		case r.URL.Path == "/init.mp4":
			if mapFailOnce.Load() {
				mapFailOnce.Store(false)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(makeFtypInit("isom"))
		case strings.HasPrefix(r.URL.Path, "/seg-"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(makeMoofSeg())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	svc := &Service{
		chunkPools:     newTestChunkPools(64*1024, 128*1024),
		chanBufferSize: 16,
	}
	playlistClient := resty.New().SetTimeout(3 * time.Second)
	segmentClient := resty.New().SetTimeout(5 * time.Second)
	fetchURL := func() (string, error) { return server.URL + "/playlist.m3u8", nil }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	chunkPool, releasePool := svc.AcquireChunkPool(10000)
	ch, err := svc.ReadHlsStream(fetchURL, playlistClient, segmentClient, ctx, 10000, chunkPool, releasePool)
	if err != nil {
		t.Fatalf("ReadHlsStream: %v", err)
	}

	sawInit := false
	sawMedia := false
	deadline := time.After(8 * time.Second)
	for !sawInit || !sawMedia {
		select {
		case data, ok := <-ch:
			if !ok {
				t.Fatal("channel closed early")
			}
			switch boxTypeOf(data) {
			case "ftyp":
				sawInit = true
			case "moof":
				sawMedia = true
			}
		case <-deadline:
			t.Fatalf("timeout sawInit=%v sawMedia=%v mapFailOnce=%v", sawInit, sawMedia, mapFailOnce.Load())
		}
	}
	// Playlist seq never advanced; after map recovers we still deliver that seq's media.
	if mediaSeq.Load() != 3000 {
		t.Fatalf("test fixture should keep mediaSeq at 3000, got %d", mediaSeq.Load())
	}
}

func TestReadHlsStream_TSWithoutMapUnaffected(t *testing.T) {
	initStreamTestConfig(t)

	var mediaSeq atomic.Int64
	mediaSeq.Store(2000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/playlist.m3u8":
			seq := mediaSeq.Load()
			body := strings.Join([]string{
				"#EXTM3U",
				"#EXT-X-VERSION:3",
				"#EXT-X-TARGETDURATION:1",
				fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d", seq),
				"#EXTINF:1.0,",
				fmt.Sprintf("seg-%d.ts", seq),
			}, "\n")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(body))
			mediaSeq.Add(1)
		case strings.HasPrefix(r.URL.Path, "/seg-"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte{0x47, 0x00, 0x00, 0x00}) // fake TS sync
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	svc := &Service{
		chunkPools:     newTestChunkPools(64*1024, 128*1024),
		chanBufferSize: 16,
	}
	playlistClient := resty.New().SetTimeout(3 * time.Second)
	segmentClient := resty.New().SetTimeout(5 * time.Second)
	fetchURL := func() (string, error) { return server.URL + "/playlist.m3u8", nil }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	chunkPool, releasePool := svc.AcquireChunkPool(10000)
	ch, err := svc.ReadHlsStream(fetchURL, playlistClient, segmentClient, ctx, 10000, chunkPool, releasePool)
	if err != nil {
		t.Fatalf("ReadHlsStream: %v", err)
	}

	got := 0
	deadline := time.After(5 * time.Second)
	for got < 2 {
		select {
		case data, ok := <-ch:
			if !ok {
				t.Fatal("channel closed early")
			}
			if boxTypeOf(data) == "ftyp" {
				t.Fatal("TS playlist must not deliver fMP4 init")
			}
			got++
		case <-deadline:
			t.Fatalf("timeout waiting for TS segments, got=%d", got)
		}
	}
}

func BenchmarkParseM3u8_SmallWindow(b *testing.B) {
	body := []byte(buildPlaylistBody(6, false))
	mon := benchreport.Start(b, int64(len(body)))
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	mon.MarkTimerStart()

	for i := 0; i < b.N; i++ {
		pl, err := hlsutil.ParseBytes(body)
		if err != nil {
			b.Fatalf("parseM3u8 failed: %v", err)
		}
		if len(pl.Segments) != 6 {
			b.Fatalf("unexpected segment count: got=%d want=6", len(pl.Segments))
		}
		mon.SamplePeriodically(i)
	}
}

func BenchmarkParseM3u8_MapWindow(b *testing.B) {
	body := []byte(buildPlaylistBody(20, true))
	mon := benchreport.Start(b, int64(len(body)))
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	mon.MarkTimerStart()

	for i := 0; i < b.N; i++ {
		pl, err := hlsutil.ParseBytes(body)
		if err != nil {
			b.Fatalf("parseM3u8 failed: %v", err)
		}
		if pl.MapURI == "" {
			b.Fatal("expected map URI")
		}
		mon.SamplePeriodically(i)
	}
}

func BenchmarkParseM3u8_FromBodyBytes(b *testing.B) {
	bodyBytes := []byte(buildPlaylistBody(20, true))
	mon := benchreport.Start(b, int64(len(bodyBytes)))
	b.ReportAllocs()
	b.SetBytes(int64(len(bodyBytes)))
	b.ResetTimer()
	mon.MarkTimerStart()

	for i := 0; i < b.N; i++ {
		pl, err := hlsutil.Parse(string(bodyBytes))
		if err != nil {
			b.Fatalf("parseM3u8 failed: %v", err)
		}
		if len(pl.Segments) != 20 {
			b.Fatalf("unexpected segment count: got=%d want=20", len(pl.Segments))
		}
		mon.SamplePeriodically(i)
	}
}

func BenchmarkParseM3u8_Scalability(b *testing.B) {
	cases := []struct {
		name     string
		segments int
		withMap  bool
	}{
		{name: "window6_noMap", segments: 6, withMap: false},
		{name: "window20_withMap", segments: 20, withMap: true},
		{name: "window100_withMap", segments: 100, withMap: true},
		{name: "window500_withMap", segments: 500, withMap: true},
	}
	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			body := []byte(buildPlaylistBody(tc.segments, tc.withMap))
			mon := benchreport.Start(b, int64(len(body)))
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			mon.MarkTimerStart()
			for i := 0; i < b.N; i++ {
				pl, err := hlsutil.ParseBytes(body)
				if err != nil {
					b.Fatalf("parseM3u8 failed: %v", err)
				}
				if len(pl.Segments) != tc.segments {
					b.Fatalf("unexpected segment count: got=%d want=%d", len(pl.Segments), tc.segments)
				}
				mon.SamplePeriodically(i)
			}
		})
	}
}
