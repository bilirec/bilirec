package convert

import "github.com/bilirec/bilirec/internal/modules/metrics"

// serviceMetrics owns conversion metric policy and records conversion events.
type serviceMetrics struct {
	exporter *metrics.Exporter
	enabled  bool
}

func newServiceMetrics(exporter *metrics.Exporter, enabled bool) *serviceMetrics {
	return &serviceMetrics{
		exporter: exporter,
		enabled:  enabled,
	}
}

func (m *serviceMetrics) taskQueued(provider Provider) {
	if m.enabled {
		m.exporter.ConvertTaskQueued(string(provider))
	}
}

func (m *serviceMetrics) taskFinished(provider Provider) {
	if m.enabled {
		m.exporter.ConvertTaskFinished(string(provider))
	}
}

func (m *serviceMetrics) taskFailed(provider Provider) {
	if m.enabled {
		m.exporter.ConvertTaskFailed(string(provider))
	}
}

func (m *serviceMetrics) taskCancelled(provider Provider) {
	if m.enabled {
		m.exporter.ConvertTaskCancelled(string(provider))
	}
}

func (m *serviceMetrics) setTaskMetrics(provider Provider, pending, processing int) {
	if m.enabled {
		providerName := string(provider)
		m.exporter.SetConvertTasksPending(providerName, pending)
		m.exporter.SetConvertTasksProcessing(providerName, processing)
	}
}
