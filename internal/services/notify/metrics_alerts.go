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

	metricsAlertWindowSlowIO          = 15 * time.Minute
	metricsAlertForSlowIO           = 10 * time.Minute
	metricsAlertThresholdSlowEvents = 2.0

	metricsAlertWindowTimeline       = 15 * time.Minute
	metricsAlertForTimeline          = 5 * time.Minute
	metricsAlertThresholdTimelineSec = 30.0

	metricsAlertTickInterval = 15 * time.Second
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

type metricsAlertFire struct {
	roomID    int
	kind      metricsAlertKind
	sum       float64
	streamer  string
	roomTitle string
}

type metricsAlerts struct {
	mu          sync.Mutex
	rooms       map[int]map[metricsAlertKind]*metricsAlertRoomState
	lastMeta    map[int]RecordingAlertContext
	onFire      func(roomID int, kind metricsAlertKind, sum float64, streamer, roomTitle string)
	tickStop    chan struct{}
	tickRunning bool
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

func (h *metricsAlerts) clearRoom(roomID int) {
	if roomID <= 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.rooms, roomID)
	delete(h.lastMeta, roomID)
	h.maybeStopTickerLocked()
}

func (h *metricsAlerts) observe(ctx RecordingAlertContext, kind metricsAlertKind, add float64) {
	params, ok := metricsAlertParamsByKind[kind]
	if !ok {
		return
	}
	h.observeWithParams(ctx, kind, add, params)
}

func (h *metricsAlerts) observeWithParams(
	ctx RecordingAlertContext,
	kind metricsAlertKind,
	add float64,
	params metricsAlertParams,
) {
	if ctx.RoomID <= 0 || add <= 0 {
		return
	}
	now := time.Now()
	var fires []metricsAlertFire
	h.mu.Lock()
	h.rememberMetaLocked(ctx)
	st := h.stateLocked(ctx.RoomID, kind)
	st.samples = append(st.samples, metricsAlertSample{at: now, v: add})
	if fire, sum := h.evaluateStateLocked(now, st, params); fire {
		fires = append(fires, h.firePayloadLocked(ctx.RoomID, kind, sum))
		st.fired = true
	}
	h.ensureTickerLocked()
	h.mu.Unlock()
	h.dispatchFires(fires)
}

func (h *metricsAlerts) tick() {
	now := time.Now()
	var fires []metricsAlertFire
	h.mu.Lock()
	for roomID, byKind := range h.rooms {
		for kind, st := range byKind {
			params, ok := metricsAlertParamsByKind[kind]
			if !ok {
				continue
			}
			if fire, sum := h.evaluateStateLocked(now, st, params); fire {
				fires = append(fires, h.firePayloadLocked(roomID, kind, sum))
				st.fired = true
			}
		}
	}
	h.maybeStopTickerLocked()
	h.mu.Unlock()
	h.dispatchFires(fires)
}

func (h *metricsAlerts) rememberMetaLocked(ctx RecordingAlertContext) {
	if h.lastMeta == nil {
		h.lastMeta = make(map[int]RecordingAlertContext)
	}
	h.lastMeta[ctx.RoomID] = ctx
}

func (h *metricsAlerts) firePayloadLocked(roomID int, kind metricsAlertKind, sum float64) metricsAlertFire {
	meta := h.lastMeta[roomID]
	return metricsAlertFire{
		roomID:    roomID,
		kind:      kind,
		sum:       sum,
		streamer:  meta.Streamer,
		roomTitle: meta.RoomTitle,
	}
}

func (h *metricsAlerts) dispatchFires(fires []metricsAlertFire) {
	if h.onFire == nil {
		return
	}
	for _, f := range fires {
		h.onFire(f.roomID, f.kind, f.sum, f.streamer, f.roomTitle)
	}
}

func (h *metricsAlerts) evaluateStateLocked(now time.Time, st *metricsAlertRoomState, params metricsAlertParams) (fire bool, sum float64) {
	window, forDur, threshold := params.window, params.forDur, params.threshold
	st.samples = pruneMetricsAlertSamples(st.samples, now, window)
	sum = sumMetricsAlertSamples(st.samples, now, window)
	if sum > threshold {
		if st.pendingSince.IsZero() {
			st.pendingSince = now
		}
		if !st.fired && now.Sub(st.pendingSince) >= forDur {
			return true, sum
		}
		return false, sum
	}
	st.pendingSince = time.Time{}
	st.fired = false
	return false, sum
}

func (h *metricsAlerts) ensureTickerLocked() {
	if h.tickRunning {
		return
	}
	h.tickRunning = true
	h.tickStop = make(chan struct{})
	stop := h.tickStop
	go func() {
		ticker := time.NewTicker(metricsAlertTickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				h.tick()
			}
		}
	}()
}

func (h *metricsAlerts) maybeStopTickerLocked() {
	if !h.hasAnySamplesLocked() {
		if h.tickRunning && h.tickStop != nil {
			close(h.tickStop)
			h.tickStop = nil
			h.tickRunning = false
		}
	}
}

func (h *metricsAlerts) hasAnySamplesLocked() bool {
	for _, byKind := range h.rooms {
		for _, st := range byKind {
			if len(st.samples) > 0 {
				return true
			}
		}
	}
	return false
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
