package recorder

import (
	"testing"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
)

func TestSortStreams_QnBeforeProfile(t *testing.T) {
	streams := []bilibili.StreamURLInfo{
		{Format: "ts", Qn: 10000, URL: "ts-hq"},
		{Format: "flv", Qn: 250, URL: "flv-lq"},
		{Format: "fmp4", Qn: 10000, URL: "fmp4-hq"},
		{Format: "flv", Qn: 10000, URL: "flv-hq"},
	}

	sortStreams(streams)

	// qn 10000 first (flv → fmp4 → ts), then qn 250
	want := []string{"flv-hq", "fmp4-hq", "ts-hq", "flv-lq"}
	for i, url := range want {
		if streams[i].URL != url {
			t.Fatalf("index %d: got %q, want %q; order=%v", i, streams[i].URL, url, streamURLs(streams))
		}
	}
}

func TestSortStreams_ProfileTiebreaksAtSameQn(t *testing.T) {
	streams := []bilibili.StreamURLInfo{
		{Format: "ts", Qn: 400, URL: "ts"},
		{Format: "fmp4", Qn: 400, URL: "fmp4"},
		{Format: "flv", Qn: 400, URL: "flv"},
	}

	sortStreams(streams)

	want := []string{"flv", "fmp4", "ts"}
	for i, url := range want {
		if streams[i].URL != url {
			t.Fatalf("index %d: got %q, want %q", i, streams[i].URL, url)
		}
	}
}

func TestSortStreams_QnWithinSameProfile(t *testing.T) {
	streams := []bilibili.StreamURLInfo{
		{Format: "flv", Qn: 250, URL: "a"},
		{Format: "flv", Qn: 10000, URL: "b"},
		{Format: "flv", Qn: 400, URL: "c"},
	}

	sortStreams(streams)

	want := []string{"b", "c", "a"}
	for i, url := range want {
		if streams[i].URL != url {
			t.Fatalf("index %d: got %q, want %q", i, streams[i].URL, url)
		}
	}
}

func TestSortStreams_QnBeatsUnknownFormat(t *testing.T) {
	streams := []bilibili.StreamURLInfo{
		{Format: "unknown", Qn: 10000, URL: "x"},
		{Format: "flv", Qn: 80, URL: "flv"},
	}

	sortStreams(streams)

	if streams[0].URL != "x" || streams[1].URL != "flv" {
		t.Fatalf("unexpected order: %v", streamURLs(streams))
	}
}

func streamURLs(streams []bilibili.StreamURLInfo) []string {
	out := make([]string, len(streams))
	for i, s := range streams {
		out[i] = s.URL
	}
	return out
}
