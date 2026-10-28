package notify

import (
	"testing"
	"time"
)

func TestMetricsAlerts_FiresAfterForDuration(t *testing.T) {
	var fired int
	fire := func(roomID int, kind metricsAlertKind, sum float64, streamer, roomTitle string) {
		fired++
		if roomID != 9 || kind != metricsAlertTimeline || sum <= 30 {
			t.Fatalf("unexpected fire room=%d kind=%d sum=%v", roomID, kind, sum)
		}
	}

	var h metricsAlerts
	h.onFire = fire

	now := time.Now()
	params := metricsAlertParams{window: time.Minute, forDur: 0, threshold: 30}
	ctx9 := RecordingAlertContext{RoomID: 9}
	h.observeWithParams(ctx9, metricsAlertTimeline, 35, params)
	if fired != 1 {
		t.Fatalf("expected immediate fire with for=0, got %d", fired)
	}

	h.observeWithParams(ctx9, metricsAlertTimeline, 1, metricsAlertParams{window: time.Minute, forDur: time.Minute, threshold: 30})
	if fired != 1 {
		t.Fatalf("should not fire again while already in violation episode, got %d", fired)
	}

	h.mu.Lock()
	st := h.stateLocked(9, metricsAlertTimeline)
	st.samples = []metricsAlertSample{{at: now.Add(-2 * time.Minute), v: 1}}
	st.pendingSince = time.Time{}
	st.fired = false
	h.mu.Unlock()

	h.observeWithParams(ctx9, metricsAlertTimeline, 35, params)
	if fired != 2 {
		t.Fatalf("expected second fire after reset, got %d", fired)
	}
}

func TestMetricsAlerts_TickFiresWithoutNewSample(t *testing.T) {
	var fired int
	var h metricsAlerts
	h.onFire = func(roomID int, kind metricsAlertKind, sum float64, streamer, roomTitle string) {
		fired++
	}

	ctx7 := RecordingAlertContext{RoomID: 7}
	h.observe(ctx7, metricsAlertTimeline, 40)
	if fired != 0 {
		t.Fatalf("should not fire before for duration, got %d", fired)
	}

	h.mu.Lock()
	st := h.stateLocked(7, metricsAlertTimeline)
	st.pendingSince = time.Now().Add(-6 * time.Minute)
	st.fired = false
	h.mu.Unlock()

	h.tick()
	if fired != 1 {
		t.Fatalf("expected tick to fire after for duration, got %d", fired)
	}
}

func TestMetricsAlerts_ClearRoomAllowsRefire(t *testing.T) {
	var fired int
	var h metricsAlerts
	h.onFire = func(roomID int, kind metricsAlertKind, sum float64, streamer, roomTitle string) {
		fired++
	}

	params := metricsAlertParams{window: time.Minute, forDur: 0, threshold: 30}
	ctx3 := RecordingAlertContext{RoomID: 3}
	h.observeWithParams(ctx3, metricsAlertTimeline, 35, params)
	if fired != 1 {
		t.Fatalf("expected first fire, got %d", fired)
	}

	h.clearRoom(3)
	h.observeWithParams(ctx3, metricsAlertTimeline, 35, params)
	if fired != 2 {
		t.Fatalf("expected refire after clear, got %d", fired)
	}
}

func TestSumMetricsAlertSamples(t *testing.T) {
	now := time.Now()
	samples := []metricsAlertSample{
		{at: now.Add(-10 * time.Minute), v: 100},
		{at: now.Add(-2 * time.Minute), v: 3},
		{at: now.Add(-1 * time.Minute), v: 4},
	}
	sum := sumMetricsAlertSamples(samples, now, 5*time.Minute)
	if sum != 7 {
		t.Fatalf("expected 7, got %v", sum)
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
