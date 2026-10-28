package hls

import (
	"testing"
	"time"
)

func TestPlaylistSequenceGapDuration(t *testing.T) {
	pl := &Playlist{
		TargetDuration: 4,
		Segments:       []Segment{{Duration: 3.5}},
	}
	got := PlaylistSequenceGapDuration(pl, 2)
	want := 8 * time.Second
	if got != want {
		t.Fatalf("got %v want %v", got, want)
	}

	pl.TargetDuration = 0
	got = PlaylistSequenceGapDuration(pl, 3)
	want = 10500 * time.Millisecond
	if got != want {
		t.Fatalf("fallback extinf: got %v want %v", got, want)
	}

	got = PlaylistSequenceGapDuration(nil, 1)
	if got != 0 {
		t.Fatalf("nil playlist: got %v", got)
	}
}

func TestStreamRunnerSequenceGapCallback(t *testing.T) {
	var got time.Duration
	runner := &StreamRunner{
		nextSeq: 10,
		onSegmentGap: func(d time.Duration) {
			got = d
		},
	}
	pl := &Playlist{
		MediaSeq:       15,
		TargetDuration: 2,
		Segments:       []Segment{{Duration: 2}},
	}
	baseSeq := pl.MediaSeq
	if baseSeq > runner.nextSeq {
		lost := baseSeq - runner.nextSeq
		if runner.onSegmentGap != nil {
			runner.onSegmentGap(PlaylistSequenceGapDuration(pl, lost))
		}
		runner.nextSeq = baseSeq
	}
	if got != 10*time.Second {
		t.Fatalf("got %v want 10s", got)
	}
	if runner.nextSeq != 15 {
		t.Fatalf("nextSeq=%d", runner.nextSeq)
	}
}
