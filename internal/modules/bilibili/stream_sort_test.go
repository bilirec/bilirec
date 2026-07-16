package bilibili

import "testing"

func TestSortStreams_QnBeforeProfile(t *testing.T) {
	streams := []StreamURLInfo{
		{Format: "ts", Qn: 10000, URL: "ts-hq"},
		{Format: "flv", Qn: 250, URL: "flv-lq"},
		{Format: "fmp4", Qn: 10000, URL: "fmp4-hq"},
		{Format: "flv", Qn: 10000, URL: "flv-hq"},
	}

	sortStreams(streams, 0)

	want := []string{"flv-hq", "fmp4-hq", "ts-hq", "flv-lq"}
	for i, url := range want {
		if streams[i].URL != url {
			t.Fatalf("index %d: got %q, want %q; order=%v", i, streams[i].URL, url, streamSortTestURLs(streams))
		}
	}
}

func TestSortStreams_ProfileTiebreaksAtSameQn(t *testing.T) {
	streams := []StreamURLInfo{
		{Format: "ts", Qn: 400, URL: "ts"},
		{Format: "fmp4", Qn: 400, URL: "fmp4"},
		{Format: "flv", Qn: 400, URL: "flv"},
	}

	sortStreams(streams, 0)

	want := []string{"flv", "fmp4", "ts"}
	for i, url := range want {
		if streams[i].URL != url {
			t.Fatalf("index %d: got %q, want %q", i, streams[i].URL, url)
		}
	}
}

func TestSortStreams_QnWithinSameProfile(t *testing.T) {
	streams := []StreamURLInfo{
		{Format: "flv", Qn: 250, URL: "a"},
		{Format: "flv", Qn: 10000, URL: "b"},
		{Format: "flv", Qn: 400, URL: "c"},
	}

	sortStreams(streams, 0)

	want := []string{"b", "c", "a"}
	for i, url := range want {
		if streams[i].URL != url {
			t.Fatalf("index %d: got %q, want %q", i, streams[i].URL, url)
		}
	}
}

func TestSortStreams_QnBeatsUnknownFormat(t *testing.T) {
	streams := []StreamURLInfo{
		{Format: "unknown", Qn: 10000, URL: "x"},
		{Format: "flv", Qn: 80, URL: "flv"},
	}

	sortStreams(streams, 0)

	if streams[0].URL != "x" || streams[1].URL != "flv" {
		t.Fatalf("unexpected order: %v", streamSortTestURLs(streams))
	}
}

func TestSortStreams_QnFirst(t *testing.T) {
	streams := []StreamURLInfo{
		{Format: "flv", Qn: 10000, URL: "flv-original"},
		{Format: "flv", Qn: 150, URL: "flv-high"},
		{Format: "fmp4", Qn: 10000, URL: "fmp4-original"},
		{Format: "flv", Qn: 250, URL: "flv-super"},
	}

	sortStreams(streams, 150)

	want := []string{"flv-high", "flv-original", "fmp4-original", "flv-super"}
	for i, url := range want {
		if streams[i].URL != url {
			t.Fatalf("index %d: got %q, want %q; order=%v", i, streams[i].URL, url, streamSortTestURLs(streams))
		}
	}
}

func TestParseStreamProfiles(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got, err := ParseStreamProfiles("")
		if err != nil || got != nil {
			t.Fatalf("got=%v err=%v", got, err)
		}
	})

	t.Run("single", func(t *testing.T) {
		got, err := ParseStreamProfiles("hls-fmp4")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0] != ProfileHLSFMP4 {
			t.Fatalf("got=%v", got)
		}
	})

	t.Run("multi dedupe", func(t *testing.T) {
		got, err := ParseStreamProfiles("hls-fmp4, http-flv, hls-fmp4")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0] != ProfileHLSFMP4 || got[1] != ProfileHTTPFLV {
			t.Fatalf("got=%v", got)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		_, err := ParseStreamProfiles("hls-fmp4,dash")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestNormalizeStreamProfiles(t *testing.T) {
	got, err := NormalizeStreamProfiles([]string{" hls-fmp4 ", "http-flv", "hls-fmp4"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != ProfileHLSFMP4 || got[1] != ProfileHTTPFLV {
		t.Fatalf("got=%v", got)
	}
}

func streamSortTestURLs(streams []StreamURLInfo) []string {
	out := make([]string, len(streams))
	for i, s := range streams {
		out[i] = s.URL
	}
	return out
}
