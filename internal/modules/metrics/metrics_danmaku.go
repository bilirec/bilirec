package metrics

const (
	metricDanmakuRecordingActive         = "bilirec_danmaku_recording_active"
	metricDanmakuSessionsTotal           = "bilirec_danmaku_sessions_total"
	metricDanmakuConnectionActive        = "bilirec_danmaku_connection_active"
	metricDanmakuConnectionAttemptsTotal = "bilirec_danmaku_connection_attempts_total"
	metricDanmakuReconnectsTotal         = "bilirec_danmaku_reconnects_total"
	metricDanmakuMessagesTotal           = "bilirec_danmaku_messages_total"
	metricDanmakuMessagesDroppedTotal    = "bilirec_danmaku_messages_dropped_total"
	metricDanmakuParseErrorsTotal        = "bilirec_danmaku_parse_errors_total"
	metricDanmakuBytesTotal              = "bilirec_danmaku_bytes_total"
	metricDanmakuRotationsTotal          = "bilirec_danmaku_rotations_total"
	metricDanmakuRotationDroppedTotal    = "bilirec_danmaku_rotation_dropped_total"
)

const (
	eventTypeDanmaku   = "danmaku"
	eventTypeSuperChat = "super_chat"
	eventTypeGift      = "gift"
	eventTypeGuard     = "guard"
)

var danmakuEventTypes = [...]string{
	eventTypeDanmaku,
	eventTypeSuperChat,
	eventTypeGift,
	eventTypeGuard,
}

func isDanmakuEventType(eventType string) bool {
	switch eventType {
	case eventTypeDanmaku, eventTypeSuperChat, eventTypeGift, eventTypeGuard:
		return true
	default:
		return false
	}
}

// DanmakuSessionStarted records a danmaku sidecar session and marks it active.
func (e *Exporter) DanmakuSessionStarted(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.counter(metricDanmakuSessionsTotal, roomID).Inc()
	e.registry.gauge(metricDanmakuRecordingActive, roomID).Set(1)
}

// DanmakuSessionStopped marks the danmaku session and connection inactive.
func (e *Exporter) DanmakuSessionStopped(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.gauge(metricDanmakuRecordingActive, roomID).Set(0)
	e.registry.gauge(metricDanmakuConnectionActive, roomID).Set(0)
}

// DanmakuConnectionAttempt records a danmaku WebSocket connection attempt.
func (e *Exporter) DanmakuConnectionAttempt(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.counter(metricDanmakuConnectionAttemptsTotal, roomID).Inc()
}

// DanmakuConnectionActive updates the danmaku WebSocket connection status.
func (e *Exporter) DanmakuConnectionActive(roomID int, active bool) {
	if e.registry == nil {
		return
	}
	if active {
		e.registry.gauge(metricDanmakuConnectionActive, roomID).Set(1)
	} else {
		e.registry.gauge(metricDanmakuConnectionActive, roomID).Set(0)
	}
}

// DanmakuReconnect records a danmaku WebSocket reconnect.
func (e *Exporter) DanmakuReconnect(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.counter(metricDanmakuReconnectsTotal, roomID).Inc()
}

// DanmakuMessageReceived records a received danmaku event.
func (e *Exporter) DanmakuMessageReceived(roomID int, eventType string) {
	if e.registry == nil || !isDanmakuEventType(eventType) {
		return
	}
	e.registry.counterEvent(metricDanmakuMessagesTotal, roomID, eventType).Inc()
}

// DanmakuMessageDropped records a danmaku event dropped by the queue.
func (e *Exporter) DanmakuMessageDropped(roomID int, eventType string) {
	if e.registry == nil || !isDanmakuEventType(eventType) {
		return
	}
	e.registry.counterEvent(metricDanmakuMessagesDroppedTotal, roomID, eventType).Inc()
}

// DanmakuParseError records an unparsable ordinary danmaku event.
func (e *Exporter) DanmakuParseError(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.counter(metricDanmakuParseErrorsTotal, roomID).Inc()
}

// AddDanmakuBytes accumulates encoded bytes sent to the sidecar writer.
func (e *Exporter) AddDanmakuBytes(roomID int, n int) {
	if e.registry == nil {
		return
	}
	e.registry.counter(metricDanmakuBytesTotal, roomID).Add(n)
}

// DanmakuRotation records a successfully queued writer rotation notice.
func (e *Exporter) DanmakuRotation(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.counter(metricDanmakuRotationsTotal, roomID).Inc()
}

// DanmakuRotationDropped records a writer rotation notice dropped by a full queue.
func (e *Exporter) DanmakuRotationDropped(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.counter(metricDanmakuRotationDroppedTotal, roomID).Inc()
}

// UnregisterDanmakuRoom drops the room's danmaku gauges so stopped rooms
// disappear from current-state queries. Counters stay so increase() can
// accumulate across sessions in the same process.
func (e *Exporter) UnregisterDanmakuRoom(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.unregisterGauge(metricDanmakuRecordingActive, roomID)
	e.registry.unregisterGauge(metricDanmakuConnectionActive, roomID)
}

func (e *Exporter) unregisterDanmakuCounters(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.unregisterCounter(metricDanmakuSessionsTotal, roomID)
	e.registry.unregisterCounter(metricDanmakuConnectionAttemptsTotal, roomID)
	e.registry.unregisterCounter(metricDanmakuReconnectsTotal, roomID)
	e.registry.unregisterCounter(metricDanmakuParseErrorsTotal, roomID)
	e.registry.unregisterCounter(metricDanmakuBytesTotal, roomID)
	e.registry.unregisterCounter(metricDanmakuRotationsTotal, roomID)
	e.registry.unregisterCounter(metricDanmakuRotationDroppedTotal, roomID)
	for _, eventType := range danmakuEventTypes {
		e.registry.unregisterCounterEvent(metricDanmakuMessagesTotal, roomID, eventType)
		e.registry.unregisterCounterEvent(metricDanmakuMessagesDroppedTotal, roomID, eventType)
	}
}
