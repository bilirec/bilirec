package metrics

const (
	metricActiveRecordings           = "bilirec_active_recordings"
	metricRoomStreamBytesTotal       = "bilirec_room_stream_bytes_total"
	metricRoomRecordingSessionsTotal = "bilirec_room_recording_sessions_total"
	metricRoomStreamRecoveryTotal    = "bilirec_room_stream_recovery_total"
	metricRoomRecordingActive        = "bilirec_room_recording_active"
)

// AddStreamBytes accumulates the number of bytes written for a room.
func (e *Exporter) AddStreamBytes(roomID int, n int) {
	if e.registry == nil {
		return
	}
	e.registry.counter(metricRoomStreamBytesTotal, roomID).Add(n)
}

// RecordingStarted records a session, marks recording as active, and updates
// the room metadata.
func (e *Exporter) RecordingStarted(roomID int, uname string) {
	if e.registry == nil {
		return
	}
	e.registry.counter(metricRoomRecordingSessionsTotal, roomID).Inc()
	e.registry.gauge(metricRoomRecordingActive, roomID).Set(1)
	e.registry.globalGauge(metricActiveRecordings).Add(1)
	e.registry.updateRoomInfo(roomID, uname)
}

// RecordingStopped marks the room recording as inactive.
func (e *Exporter) RecordingStopped(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.gauge(metricRoomRecordingActive, roomID).Set(0)
	e.registry.globalGauge(metricActiveRecordings).Add(-1)
}

// AddRecovery records a stream recovery attempt.
func (e *Exporter) AddRecovery(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.counter(metricRoomStreamRecoveryTotal, roomID).Inc()
}

func (e *Exporter) UnregisterRecorderRoom(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.unregisterCounter(metricRoomStreamBytesTotal, roomID)
	e.registry.unregisterCounter(metricRoomRecordingSessionsTotal, roomID)
	e.registry.unregisterCounter(metricRoomStreamRecoveryTotal, roomID)
	e.registry.unregisterGauge(metricRoomRecordingActive, roomID)
}
