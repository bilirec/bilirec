package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestStreamObservation_RecordTimelineGap(t *testing.T) {
	e := newExporter(t, true)
	sess := e.StreamObservation(42, StreamObservationMeta{})
	sess.RecordTimelineGap(2 * time.Second)
	sess.RecordTimelineGap(3 * time.Second)
	sess.RecordTimelineGap(1500 * time.Millisecond)

	out := e.scrape()
	if !strings.Contains(out, `bilirec_room_stream_timestamp_jumps_total{room_id="42"} 3`) {
		t.Fatalf("expected 3 jumps (no ingest dedup on VM), scrape:\n%s", out)
	}
	if !strings.Contains(out, `bilirec_room_stream_timestamp_jump_collapsed_seconds_total{room_id="42"} 6.5`) {
		t.Fatalf("expected 6.5 collapsed seconds, scrape:\n%s", out)
	}
}

func TestStreamObservation_RecordFlushThreshold(t *testing.T) {
	e := newExporter(t, true)
	sess := e.StreamObservation(5, StreamObservationMeta{})
	sess.RecordFlush(2 * time.Second)
	sess.RecordFlush(4 * time.Second)
	out := e.scrape()
	if strings.Contains(out, `bilirec_room_stream_slow_flushes_total{room_id="5"} 2`) {
		t.Fatalf("only flush >3s should count, scrape:\n%s", out)
	}
	if !strings.Contains(out, `bilirec_room_stream_slow_flushes_total{room_id="5"} 1`) {
		t.Fatalf("expected 1 slow flush, scrape:\n%s", out)
	}
}
