package notify

import "sync"

const (
	metricsAlertThresholdEnqueueSec = 2.0
	// metricsAlertThresholdSlowEvents counts slow flushes and slow syncs together.
	// The first alert fires when the total reaches 3, then each time another 3 accumulate.
	metricsAlertThresholdSlowEvents  = 3.0
	metricsAlertThresholdTimelineSec = 30.0
)

type metricsAlertKind uint8

const (
	metricsAlertEnqueue metricsAlertKind = iota
	metricsAlertSlowIO
	metricsAlertTimeline
)

type metricsAlertRoomState struct {
	sum    float64
	bucket int
}

type metricsAlerts struct {
	mu     sync.Mutex
	rooms  map[int]map[metricsAlertKind]*metricsAlertRoomState
	onFire func(roomID int, kind metricsAlertKind, sum float64, streamer, roomTitle string)
}

func (h *metricsAlerts) clearRoom(roomID int) {
	if roomID <= 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.rooms, roomID)
}

func (h *metricsAlerts) observe(ctx RecordingAlertContext, kind metricsAlertKind, add float64) {
	threshold, ok := metricsAlertThreshold(kind)
	if !ok || ctx.RoomID <= 0 || add <= 0 {
		return
	}
	h.mu.Lock()
	st := h.stateLocked(ctx.RoomID, kind)
	st.sum += add
	next := int(st.sum / threshold)
	fire := false
	sum := st.sum
	if next > st.bucket {
		st.bucket = next
		fire = true
	}
	h.mu.Unlock()
	if fire && h.onFire != nil {
		h.onFire(ctx.RoomID, kind, sum, ctx.Streamer, ctx.RoomTitle)
	}
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

func metricsAlertThreshold(kind metricsAlertKind) (float64, bool) {
	switch kind {
	case metricsAlertEnqueue:
		return metricsAlertThresholdEnqueueSec, true
	case metricsAlertSlowIO:
		return metricsAlertThresholdSlowEvents, true
	case metricsAlertTimeline:
		return metricsAlertThresholdTimelineSec, true
	default:
		return 0, false
	}
}
