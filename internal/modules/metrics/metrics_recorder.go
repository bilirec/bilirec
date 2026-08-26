package metrics

const (
	metricActiveRecordings                    = "bilirec_active_recordings"
	metricRoomStreamBytesTotal                = "bilirec_room_stream_bytes_total"
	metricRoomRecordingSessionsTotal          = "bilirec_room_recording_sessions_total"
	metricRoomStreamRecoveryTotal             = "bilirec_room_stream_recovery_total"
	metricRoomRecordingActive                 = "bilirec_room_recording_active"
	metricRoomStreamConnectionActive          = "bilirec_room_stream_connection_active"
	metricRoomStreamConnectAttemptsTotal      = "bilirec_room_stream_connect_attempts_total"
	metricRoomRecordingRotationsTotal         = "bilirec_room_recording_rotations_total"
	metricRoomRecordingStartFailuresTotal     = "bilirec_room_recording_start_failures_total"
	metricRoomStreamRecoverySuccessTotal      = "bilirec_room_stream_recovery_success_total"
	metricRoomRecordingGaveUpTotal            = "bilirec_room_recording_gave_up_total"
	metricRoomRecordingSegmentsDiscardedTotal = "bilirec_room_recording_segments_discarded_total"
	metricRoomRecordingPipelineErrorsTotal    = "bilirec_room_recording_pipeline_errors_total"
)

const (
	ReasonNotLive        = "not_live"
	ReasonEmptyURLs      = "empty_urls"
	ReasonUnreachable    = "unreachable"
	ReasonDisk           = "disk"
	ReasonConcurrent     = "concurrent"
	ReasonBanned         = "banned"
	ReasonEncrypted      = "encrypted"
	ReasonAPI            = "api"
	ReasonOther          = "other"
	ReasonMaxAttempts    = "max_attempts"
	ReasonNotLiveTimeout = "not_live_timeout"
	ReasonTiny           = "tiny"
	ReasonSkippedSmall   = "skipped_small"
	ReasonStatError      = "stat_error"
	ReasonNotFLV         = "not_flv"
	ReasonOpen           = "open"
)

var (
	startFailureReasons = [...]string{
		ReasonNotLive,
		ReasonEmptyURLs,
		ReasonUnreachable,
		ReasonDisk,
		ReasonConcurrent,
		ReasonBanned,
		ReasonEncrypted,
		ReasonAPI,
		ReasonOther,
	}

	gaveUpReasons = [...]string{
		ReasonMaxAttempts,
		ReasonNotLiveTimeout,
		ReasonConcurrent,
		ReasonBanned,
		ReasonEncrypted,
	}

	segmentDiscardedReasons = [...]string{
		ReasonTiny,
		ReasonSkippedSmall,
		ReasonStatError,
	}

	pipelineErrorReasons = [...]string{
		ReasonNotFLV,
		ReasonOpen,
		ReasonOther,
	}
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
	e.registry.gauge(metricRoomStreamConnectionActive, roomID).Set(0)
	e.registry.globalGauge(metricActiveRecordings).Add(-1)
}

// AddRecovery records a stream recovery attempt.
func (e *Exporter) AddRecovery(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.counter(metricRoomStreamRecoveryTotal, roomID).Inc()
}

// StreamConnectionActive updates whether the room is currently reading a live stream.
func (e *Exporter) StreamConnectionActive(roomID int, active bool) {
	if e.registry == nil {
		return
	}
	if active {
		e.registry.gauge(metricRoomStreamConnectionActive, roomID).Set(1)
	} else {
		e.registry.gauge(metricRoomStreamConnectionActive, roomID).Set(0)
	}
}

// StreamConnectAttempt records one internalStart attempt (not per CDN URL).
func (e *Exporter) StreamConnectAttempt(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.counter(metricRoomStreamConnectAttemptsTotal, roomID).Inc()
}

// RecordingRotation records a non-first user segment (live split or recovery file).
func (e *Exporter) RecordingRotation(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.counter(metricRoomRecordingRotationsTotal, roomID).Inc()
}

// RecordingStartFailed records a user/auto-record Start() failure with a fixed reason.
func (e *Exporter) RecordingStartFailed(roomID int, reason string) {
	if e.registry == nil || !isStartFailureReason(reason) {
		return
	}
	e.registry.counterReason(metricRoomRecordingStartFailuresTotal, roomID, reason).Inc()
}

// RecordingRecoverySucceeded records a recover() internalStart that connected again.
func (e *Exporter) RecordingRecoverySucceeded(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.counter(metricRoomStreamRecoverySuccessTotal, roomID).Inc()
}

// RecordingGaveUp records recover() giving up the session with a fixed reason.
func (e *Exporter) RecordingGaveUp(roomID int, reason string) {
	if e.registry == nil || !isGaveUpReason(reason) {
		return
	}
	e.registry.counterReason(metricRoomRecordingGaveUpTotal, roomID, reason).Inc()
}

// RecordingSegmentDiscarded records finalize() skipping or deleting a segment.
func (e *Exporter) RecordingSegmentDiscarded(roomID int, reason string) {
	if e.registry == nil || !isSegmentDiscardedReason(reason) {
		return
	}
	e.registry.counterReason(metricRoomRecordingSegmentsDiscardedTotal, roomID, reason).Inc()
}

// RecordingPipelineError records a pipeline abort or segment open failure.
func (e *Exporter) RecordingPipelineError(roomID int, reason string) {
	if e.registry == nil || !isPipelineErrorReason(reason) {
		return
	}
	e.registry.counterReason(metricRoomRecordingPipelineErrorsTotal, roomID, reason).Inc()
}

// UnregisterRecorderRoom drops the room's recording gauges so stopped rooms
// disappear from current-state queries. Counters stay so increase() can
// accumulate across sessions in the same process.
func (e *Exporter) UnregisterRecorderRoom(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.unregisterGauge(metricRoomRecordingActive, roomID)
	e.registry.unregisterGauge(metricRoomStreamConnectionActive, roomID)
}

func (e *Exporter) unregisterRecorderCounters(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.unregisterCounter(metricRoomStreamBytesTotal, roomID)
	e.registry.unregisterCounter(metricRoomRecordingSessionsTotal, roomID)
	e.registry.unregisterCounter(metricRoomStreamRecoveryTotal, roomID)
	e.registry.unregisterCounter(metricRoomStreamConnectAttemptsTotal, roomID)
	e.registry.unregisterCounter(metricRoomRecordingRotationsTotal, roomID)
	e.registry.unregisterCounter(metricRoomStreamRecoverySuccessTotal, roomID)
	for _, reason := range startFailureReasons {
		e.registry.unregisterCounterReason(metricRoomRecordingStartFailuresTotal, roomID, reason)
	}
	for _, reason := range gaveUpReasons {
		e.registry.unregisterCounterReason(metricRoomRecordingGaveUpTotal, roomID, reason)
	}
	for _, reason := range segmentDiscardedReasons {
		e.registry.unregisterCounterReason(metricRoomRecordingSegmentsDiscardedTotal, roomID, reason)
	}
	for _, reason := range pipelineErrorReasons {
		e.registry.unregisterCounterReason(metricRoomRecordingPipelineErrorsTotal, roomID, reason)
	}
}

func isStartFailureReason(reason string) bool {
	switch reason {
	case ReasonNotLive, ReasonEmptyURLs, ReasonUnreachable, ReasonDisk, ReasonConcurrent, ReasonBanned, ReasonEncrypted, ReasonAPI, ReasonOther:
		return true
	default:
		return false
	}
}

func isGaveUpReason(reason string) bool {
	switch reason {
	case ReasonMaxAttempts, ReasonNotLiveTimeout, ReasonConcurrent, ReasonBanned, ReasonEncrypted:
		return true
	default:
		return false
	}
}

func isSegmentDiscardedReason(reason string) bool {
	switch reason {
	case ReasonTiny, ReasonSkippedSmall, ReasonStatError:
		return true
	default:
		return false
	}
}

func isPipelineErrorReason(reason string) bool {
	switch reason {
	case ReasonNotFLV, ReasonOpen, ReasonOther:
		return true
	default:
		return false
	}
}
