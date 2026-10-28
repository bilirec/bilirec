package notify

import (
	"sync"
	"time"
)

// Thresholds aligned with public/alerts/bilirec.yml (BilirecWriterBackpressure,
// BilirecSlowDiskIO, BilirecTimestampJump).
const (
	metricsAlertWindowEnqueue       = 5 * time.Minute
	metricsAlertForEnqueue          = 5 * time.Minute
	metricsAlertThresholdEnqueueSec = 2.0

	metricsAlertWindowSlowIO        = 15 * time.Minute
	metricsAlertForSlowIO           = 10 * time.Minute
	metricsAlertThresholdSlowEvents = 2.0

	metricsAlertWindowTimeline       = 15 * time.Minute
	metricsAlertForTimeline          = 5 * time.Minute
	metricsAlertThresholdTimelineSec = 30.0
)

type metricsAlertKind uint8

const (
	metricsAlertEnqueue metricsAlertKind = iota
	metricsAlertSlowIO
	metricsAlertTimeline
)

type metricsAlertSample struct {
	at time.Time
	v  float64
}

type metricsAlertRoomState struct {
	samples      []metricsAlertSample
	pendingSince time.Time
	fired        bool
}

type metricsAlerts struct {
	mu     sync.Mutex
	rooms  map[int]map[metricsAlertKind]*metricsAlertRoomState
	onFire func(roomID int, kind metricsAlertKind, sum float64)
}

type metricsAlertParams struct {
	window    time.Duration
	forDur    time.Duration
	threshold float64
}

var metricsAlertParamsByKind = map[metricsAlertKind]metricsAlertParams{
	metricsAlertEnqueue: {
		window:    metricsAlertWindowEnqueue,
		forDur:    metricsAlertForEnqueue,
		threshold: metricsAlertThresholdEnqueueSec,
	},
	metricsAlertSlowIO: {
		window:    metricsAlertWindowSlowIO,
		forDur:    metricsAlertForSlowIO,
		threshold: metricsAlertThresholdSlowEvents,
	},
	metricsAlertTimeline: {
		window:    metricsAlertWindowTimeline,
		forDur:    metricsAlertForTimeline,
		threshold: metricsAlertThresholdTimelineSec,
	},
}

func (h *metricsAlerts) observe(roomID int, kind metricsAlertKind, add float64) {
	params, ok := metricsAlertParamsByKind[kind]
	if !ok {
		return
	}
	h.observeWithParams(roomID, kind, add, params)
}

func (h *metricsAlerts) observeWithParams(
	roomID int,
	kind metricsAlertKind,
	add float64,
	params metricsAlertParams,
) {
	if roomID <= 0 || add <= 0 {
		return
	}
	window, forDur, threshold := params.window, params.forDur, params.threshold
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()

	st := h.stateLocked(roomID, kind)
	st.samples = append(st.samples, metricsAlertSample{at: now, v: add})
	st.samples = pruneMetricsAlertSamples(st.samples, now, window)

	sum := sumMetricsAlertSamples(st.samples, now, window)
	if sum > threshold {
		if st.pendingSince.IsZero() {
			st.pendingSince = now
		}
		if !st.fired && now.Sub(st.pendingSince) >= forDur {
			if h.onFire != nil {
				h.onFire(roomID, kind, sum)
			}
			st.fired = true
		}
		return
	}
	st.pendingSince = time.Time{}
	st.fired = false
}

func (h *metricsAlerts) stateLocked(roomID int, kind metricsAlertKind) *metricsAlertRoomState {
	if h.rooms == nil {
		h.rooms = make(map[int]map[metricsAlertKind]*metricsAlertRoomState)
	}
	byKind, ok := h.rooms[roomID]
	if !ok {
		byKind = make(map[metricsAlertKind]*metricsAlertRoomState)
		h.rooms[roomID] = byKind
	}
	st, ok := byKind[kind]
	if !ok {
		st = &metricsAlertRoomState{}
		byKind[kind] = st
	}
	return st
}

func pruneMetricsAlertSamples(samples []metricsAlertSample, now time.Time, window time.Duration) []metricsAlertSample {
	cutoff := now.Add(-window)
	i := 0
	for _, s := range samples {
		if !s.at.Before(cutoff) {
			samples[i] = s
			i++
		}
	}
	return samples[:i]
}

func sumMetricsAlertSamples(samples []metricsAlertSample, now time.Time, window time.Duration) float64 {
	cutoff := now.Add(-window)
	var sum float64
	for _, s := range samples {
		if !s.at.Before(cutoff) {
			sum += s.v
		}
	}
	return sum
}
