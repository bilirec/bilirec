package notify

import "testing"

func TestMetricsAlerts_FiresEachThresholdBucket(t *testing.T) {
	var fired []float64
	var h metricsAlerts
	h.onFire = func(roomID int, kind metricsAlertKind, sum float64, streamer, roomTitle string) {
		if roomID != 9 || kind != metricsAlertTimeline || streamer != "a" || roomTitle != "t" {
			t.Fatalf("unexpected fire room=%d kind=%d streamer=%q title=%q", roomID, kind, streamer, roomTitle)
		}
		fired = append(fired, sum)
	}

	ctx := RecordingAlertContext{RoomID: 9, Streamer: "a", RoomTitle: "t"}
	h.observe(ctx, metricsAlertTimeline, 29)
	if len(fired) != 0 {
		t.Fatalf("below first bucket should not fire, got %v", fired)
	}

	h.observe(ctx, metricsAlertTimeline, 1)
	if len(fired) != 1 || fired[0] != 30 {
		t.Fatalf("crossing 30s should fire once, got %v", fired)
	}

	h.observe(ctx, metricsAlertTimeline, 20)
	if len(fired) != 1 {
		t.Fatalf("50s should stay in the first bucket, got %v", fired)
	}

	h.observe(ctx, metricsAlertTimeline, 10)
	if len(fired) != 2 || fired[1] != 60 {
		t.Fatalf("crossing 60s should fire again, got %v", fired)
	}
}

func TestMetricsAlerts_JumpSkipsExtraFire(t *testing.T) {
	var fired int
	var h metricsAlerts
	h.onFire = func(roomID int, kind metricsAlertKind, sum float64, streamer, roomTitle string) {
		fired++
		if kind != metricsAlertTimeline || sum != 45 {
			t.Fatalf("unexpected fire kind=%d sum=%v", kind, sum)
		}
	}

	h.observe(RecordingAlertContext{RoomID: 4}, metricsAlertTimeline, 45)
	if fired != 1 {
		t.Fatalf("a jump across two buckets should fire once, got %d", fired)
	}
}

func TestMetricsAlerts_ClearRoomAllowsRefire(t *testing.T) {
	var fired int
	var h metricsAlerts
	h.onFire = func(roomID int, kind metricsAlertKind, sum float64, streamer, roomTitle string) {
		fired++
	}

	ctx := RecordingAlertContext{RoomID: 3}
	h.observe(ctx, metricsAlertTimeline, 30)
	if fired != 1 {
		t.Fatalf("expected first fire, got %d", fired)
	}

	h.clearRoom(3)
	h.observe(ctx, metricsAlertTimeline, 30)
	if fired != 2 {
		t.Fatalf("expected refire after clear, got %d", fired)
	}
}

func TestMetricsAlerts_EnqueueAndSlowIOBuckets(t *testing.T) {
	var kinds []metricsAlertKind
	var h metricsAlerts
	h.onFire = func(roomID int, kind metricsAlertKind, sum float64, streamer, roomTitle string) {
		kinds = append(kinds, kind)
	}

	enqueue := RecordingAlertContext{RoomID: 1}
	h.observe(enqueue, metricsAlertEnqueue, 1.9)
	h.observe(enqueue, metricsAlertEnqueue, 0.2)
	if len(kinds) != 1 || kinds[0] != metricsAlertEnqueue {
		t.Fatalf("enqueue should fire when wait crosses 2s, got %v", kinds)
	}
	h.observe(enqueue, metricsAlertEnqueue, 1.5)
	if len(kinds) != 1 {
		t.Fatalf("3.6s of enqueue wait should stay in the first bucket, got %v", kinds)
	}

	slow := RecordingAlertContext{RoomID: 2}
	h.observe(slow, metricsAlertSlowIO, 1)
	h.observe(slow, metricsAlertSlowIO, 1)
	if len(kinds) != 1 {
		t.Fatalf("two slow events should not fire, got %v", kinds)
	}
	h.observe(slow, metricsAlertSlowIO, 1)
	if len(kinds) != 2 || kinds[1] != metricsAlertSlowIO {
		t.Fatalf("third slow event should fire, got %v", kinds)
	}
}

func TestFormatTimelineGapAmount(t *testing.T) {
	if got := formatTimelineGapAmount(45); got != "45 秒" {
		t.Fatalf("got %q", got)
	}
	if got := formatTimelineGapAmount(120); got != "2.0 分钟" {
		t.Fatalf("got %q", got)
	}
}
