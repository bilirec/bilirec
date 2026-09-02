package metrics

const (
	metricVictoriaLogsDroppedTotal        = "bilirec_victorialogs_logs_dropped_total"
	metricVictoriaLogsRequestsFailedTotal = "bilirec_victorialogs_requests_failed_total"
	metricVictoriaLogsRetriesTotal        = "bilirec_victorialogs_retries_total"
	metricVictoriaLogsQueueBytes          = "bilirec_victorialogs_queue_bytes"
)

// AddVictoriaLogsQueueBytes adjusts the bytes currently waiting in the remote-log queue.
func (e *Exporter) AddVictoriaLogsQueueBytes(n int) {
	if e.registry == nil {
		return
	}
	e.registry.globalGauge(metricVictoriaLogsQueueBytes).Add(float64(n))
}

// VictoriaLogsLogDropped records a log line discarded because the remote-log queue was full.
func (e *Exporter) VictoriaLogsLogDropped() {
	if e.registry == nil {
		return
	}
	e.registry.globalCounter(metricVictoriaLogsDroppedTotal).Inc()
}

// VictoriaLogsRequestFailed records an unsuccessful remote-log HTTP request.
func (e *Exporter) VictoriaLogsRequestFailed() {
	if e.registry == nil {
		return
	}
	e.registry.globalCounter(metricVictoriaLogsRequestsFailedTotal).Inc()
}

// VictoriaLogsRetry records an additional attempt to send a failed log batch.
func (e *Exporter) VictoriaLogsRetry() {
	if e.registry == nil {
		return
	}
	e.registry.globalCounter(metricVictoriaLogsRetriesTotal).Inc()
}
