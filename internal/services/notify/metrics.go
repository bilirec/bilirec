package notify

import "time"

// ObserveRecordingTimelineGap accumulates collapsed media time; notifies when aligned with BilirecTimestampJump.
func (s *Service) ObserveRecordingTimelineGap(roomID int, delta time.Duration) {
	if delta <= 0 {
		return
	}
	s.metricsAlerts.observe(roomID, metricsAlertTimeline, delta.Seconds())
}

// ObserveRecordingSlowFlush counts slow flush events toward BilirecSlowDiskIO.
func (s *Service) ObserveRecordingSlowFlush(roomID int) {
	s.metricsAlerts.observe(roomID, metricsAlertSlowIO, 1)
}

// ObserveRecordingSlowSync counts slow fsync events toward BilirecSlowDiskIO.
func (s *Service) ObserveRecordingSlowSync(roomID int) {
	s.metricsAlerts.observe(roomID, metricsAlertSlowIO, 1)
}

// ObserveRecordingEnqueueWait accumulates queue wait time toward BilirecWriterBackpressure.
func (s *Service) ObserveRecordingEnqueueWait(roomID int, wait time.Duration) {
	if wait <= 0 {
		return
	}
	s.metricsAlerts.observe(roomID, metricsAlertEnqueue, wait.Seconds())
}

func (s *Service) fireMetricsAlert(roomID int, kind metricsAlertKind, sum float64) {
	var message string
	switch kind {
	case metricsAlertEnqueue:
		message = "写入队列持续堵塞"
	case metricsAlertSlowIO:
		message = "flush / fsync 过慢"
	case metricsAlertTimeline:
		message = "媒体时间轴大幅跳变"
	default:
		return
	}
	log.Infof("指标告警 room=%d：%s（窗口累计 %.2f）", roomID, message, sum)
	// Web Push / SSE for metrics alerts: p2
	_ = sum
}
