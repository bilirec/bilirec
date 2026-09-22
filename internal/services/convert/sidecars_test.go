package convert

import "testing"

func TestServiceSidecarsNilFieldsAreNoOp(t *testing.T) {
	s := &serviceSidecars{}
	s.metricsTaskQueued(ProviderFFmpeg)
	s.metricsTaskFinished(ProviderFFmpeg)
	s.metricsTaskFailed(ProviderFFmpeg)
	s.metricsTaskCancelled(ProviderFFmpeg)
	s.metricsSetTaskGauges(ProviderFFmpeg, 1, 1)
	s.webhookConvertSucceeded(&TaskQueue{InputPath: "a", OutputPath: "b"})
}
