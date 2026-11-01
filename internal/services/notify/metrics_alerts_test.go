package notify

import (
	"testing"
	"time"
)

func TestMetricsAlerts_FiresAfterForDuration(t *testing.T) {
	var fired int
	fire := func(roomID int, kind metricsAlertKind, sum float64) {
		fired++
		if roomID != 9 || kind != metricsAlertTimeline || sum <= 30 {
			t.Fatalf("unexpected fire room=%d kind=%d sum=%v", roomID, kind, sum)
		}
	}

	var h metricsAlerts
	h.onFire = fire

	now := time.Now()
	params := metricsAlertParams{window: time.Minute, forDur: 0, threshold: 30}
	h.observeWithParams(9, metricsAlertTimeline, 35, params)
	if fired != 1 {
		t.Fatalf("expected immediate fire with for=0, got %d", fired)
	}

	h.observeWithParams(9, metricsAlertTimeline, 1, metricsAlertParams{window: time.Minute, forDur: time.Minute, threshold: 30})
	if fired != 1 {
		t.Fatalf("should not fire again while already in violation episode, got %d", fired)
	}

	h.mu.Lock()
	st := h.stateLocked(9, metricsAlertTimeline)
	st.samples = []metricsAlertSample{{at: now.Add(-2 * time.Minute), v: 1}}
	st.pendingSince = time.Time{}
	st.fired = false
	h.mu.Unlock()

	h.observeWithParams(9, metricsAlertTimeline, 35, params)
	if fired != 2 {
		t.Fatalf("expected second fire after reset, got %d", fired)
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
