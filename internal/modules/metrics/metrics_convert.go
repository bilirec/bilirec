package metrics

const (
	metricConvertTasksQueuedTotal    = "bilirec_convert_tasks_queued_total"
	metricConvertTasksCompletedTotal = "bilirec_convert_tasks_completed_total"
	metricConvertTasksFailedTotal    = "bilirec_convert_tasks_failed_total"
	metricConvertTasksCancelledTotal = "bilirec_convert_tasks_cancelled_total"
	metricConvertTasksPending        = "bilirec_convert_tasks_pending"
	metricConvertTasksProcessing     = "bilirec_convert_tasks_processing"
)

// ConvertTaskQueued records a successfully queued conversion task.
func (e *Exporter) ConvertTaskQueued(provider string) {
	if e.registry == nil {
		return
	}
	e.registry.providerCounter(metricConvertTasksQueuedTotal, provider).Inc()
}

// ConvertTaskFinished records a successful conversion.
func (e *Exporter) ConvertTaskFinished(provider string) {
	if e.registry == nil {
		return
	}
	e.registry.providerCounter(metricConvertTasksCompletedTotal, provider).Inc()
}

// ConvertTaskFailed records a failed conversion attempt.
func (e *Exporter) ConvertTaskFailed(provider string) {
	if e.registry == nil {
		return
	}
	e.registry.providerCounter(metricConvertTasksFailedTotal, provider).Inc()
}

// ConvertTaskCancelled records a conversion task cancellation.
func (e *Exporter) ConvertTaskCancelled(provider string) {
	if e.registry == nil {
		return
	}
	e.registry.providerCounter(metricConvertTasksCancelledTotal, provider).Inc()
}

// SetConvertTasksPending records the number of tasks waiting for local work.
func (e *Exporter) SetConvertTasksPending(provider string, count int) {
	if e.registry == nil {
		return
	}
	e.registry.providerGauge(metricConvertTasksPending, provider).Set(float64(count))
}

// SetConvertTasksProcessing records the number of active conversion tasks.
func (e *Exporter) SetConvertTasksProcessing(provider string, count int) {
	if e.registry == nil {
		return
	}
	e.registry.providerGauge(metricConvertTasksProcessing, provider).Set(float64(count))
}
