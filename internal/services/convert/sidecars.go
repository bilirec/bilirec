package convert

import (
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/internal/services/webhook"
)

// serviceSidecars holds optional side-path dependencies for ffmpeg/cloud managers.
// Nil fields are off; enablement is decided in convert.NewService only.
type serviceSidecars struct {
	exporter *metrics.Exporter
	webhook  *webhook.Service
}

func (s *serviceSidecars) metricsTaskQueued(provider Provider) {
	if s.exporter == nil {
		return
	}
	s.exporter.ConvertTaskQueued(string(provider))
}

func (s *serviceSidecars) metricsTaskFinished(provider Provider) {
	if s.exporter == nil {
		return
	}
	s.exporter.ConvertTaskFinished(string(provider))
}

func (s *serviceSidecars) metricsTaskFailed(provider Provider) {
	if s.exporter == nil {
		return
	}
	s.exporter.ConvertTaskFailed(string(provider))
}

func (s *serviceSidecars) metricsTaskCancelled(provider Provider) {
	if s.exporter == nil {
		return
	}
	s.exporter.ConvertTaskCancelled(string(provider))
}

func (s *serviceSidecars) metricsSetTaskGauges(provider Provider, pending, processing int) {
	if s.exporter == nil {
		return
	}
	providerName := string(provider)
	s.exporter.SetConvertTasksPending(providerName, pending)
	s.exporter.SetConvertTasksProcessing(providerName, processing)
}

func (s *serviceSidecars) webhookConvertSucceeded(queue *TaskQueue) {
	if s.webhook == nil || queue == nil {
		return
	}
	s.webhook.OnConvertTaskSuccess(queue.InputPath, queue.OutputPath)
}
