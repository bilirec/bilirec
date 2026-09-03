package logging

import "github.com/bilirec/bilirec/internal/modules/metrics"

// moduleMetrics owns VictoriaLogs metric policy and records remote-log events.
type moduleMetrics struct {
	exporter *metrics.Exporter
	enabled  bool
}

func newModuleMetrics(exporter *metrics.Exporter, enabled bool) *moduleMetrics {
	return &moduleMetrics{
		exporter: exporter,
		enabled:  enabled,
	}
}

func (m *moduleMetrics) addQueueBytes(n int) {
	if m.enabled {
		m.exporter.AddVictoriaLogsQueueBytes(n)
	}
}

func (m *moduleMetrics) logDropped() {
	if m.enabled {
		m.exporter.VictoriaLogsLogDropped()
	}
}

func (m *moduleMetrics) requestFailed() {
	if m.enabled {
		m.exporter.VictoriaLogsRequestFailed()
	}
}

func (m *moduleMetrics) retry() {
	if m.enabled {
		m.exporter.VictoriaLogsRetry()
	}
}
