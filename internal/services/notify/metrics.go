package notify

import (
	"fmt"
	"math"
	"time"

	"github.com/bilirec/bilirec/internal/modules/metrics"
)

// RecordingAlertContext identifies a room for metrics alert aggregation and push payloads.
type RecordingAlertContext struct {
	RoomID    int
	Streamer  string
	RoomTitle string
}

const (
	metricsEventTimelineGap  = "metrics_timeline_gap"
	metricsEventSlowIO       = "metrics_slow_io"
	metricsEventEnqueueWait  = "metrics_enqueue_wait"
)

// ClearRecordingMetricsAlerts drops in-memory alert state when a recording session ends.
func (s *Service) ClearRecordingMetricsAlerts(roomID int) {
	s.metricsAlerts.clearRoom(roomID)
}

// ObserveRecordingTimelineGap accumulates collapsed media time; notifies when aligned with BilirecTimestampJump.
func (s *Service) ObserveRecordingTimelineGap(ctx RecordingAlertContext, delta time.Duration) {
	if delta <= 0 || ctx.RoomID <= 0 {
		return
	}
	s.metricsAlerts.observe(ctx, metricsAlertTimeline, delta.Seconds())
}

// ObserveRecordingSlowFlush counts slow flush events toward BilirecSlowDiskIO.
func (s *Service) ObserveRecordingSlowFlush(ctx RecordingAlertContext) {
	if ctx.RoomID <= 0 {
		return
	}
	s.metricsAlerts.observe(ctx, metricsAlertSlowIO, 1)
}

// ObserveRecordingSlowSync counts slow fsync events toward BilirecSlowDiskIO.
func (s *Service) ObserveRecordingSlowSync(ctx RecordingAlertContext) {
	if ctx.RoomID <= 0 {
		return
	}
	s.metricsAlerts.observe(ctx, metricsAlertSlowIO, 1)
}

// ObserveRecordingEnqueueWait accumulates queue wait time toward BilirecWriterBackpressure.
func (s *Service) ObserveRecordingEnqueueWait(ctx RecordingAlertContext, wait time.Duration) {
	if wait <= 0 || ctx.RoomID <= 0 {
		return
	}
	s.metricsAlerts.observe(ctx, metricsAlertEnqueue, wait.Seconds())
}

func (s *Service) fireMetricsAlert(roomID int, kind metricsAlertKind, sum float64, streamer, roomTitle string) {
	eventType, message, alertValue := metricsAlertEventPayload(kind, sum)
	if eventType == "" {
		return
	}

	log.Infof("指标告警 room=%d：%s（窗口累计 %.2f）", roomID, message, sum)

	s.publishJSON(Event{
		Type:         eventType,
		RoomID:       roomID,
		Streamer:     streamer,
		RoomTitle:    roomTitle,
		Message:      message,
		Timestamp:    time.Now().Unix(),
		AlertSeconds: alertValue,
	})
}

func metricsAlertEventPayload(kind metricsAlertKind, sum float64) (eventType, message string, alertValue float64) {
	switch kind {
	case metricsAlertEnqueue:
		return metricsEventEnqueueWait,
			fmt.Sprintf("近 5 分钟写入队列累计等待 %.1f 秒，磁盘可能跟不上码率，录制可能卡顿。", sum),
			sum
	case metricsAlertSlowIO:
		count := int(math.Round(sum))
		return metricsEventSlowIO,
			fmt.Sprintf("近 15 分钟慢速 flush / fsync 共 %d 次，可能是磁盘或手机存储繁忙。", count),
			sum
	case metricsAlertTimeline:
		return metricsEventTimelineGap,
			fmt.Sprintf("近 15 分钟画面时间轴被跳过约 %s。下载没跟上直播，跳过的片段无法补回；录制时少做占带宽的事，或下一场调低画质。", formatTimelineGapAmount(sum)),
			sum
	default:
		return "", "", 0
	}
}

func formatTimelineGapAmount(seconds float64) string {
	if seconds < 90 {
		return fmt.Sprintf("%.0f 秒", math.Round(seconds))
	}
	minutes := seconds / 60
	if minutes < 10 {
		return fmt.Sprintf("%.1f 分钟", minutes)
	}
	return fmt.Sprintf("%.0f 分钟", math.Round(minutes))
}

type streamNotifyObserver struct {
	svc *Service
}

func (o *streamNotifyObserver) OnBytesWritten(_ int, _ metrics.StreamObservationMeta, _ int) {}

func (o *streamNotifyObserver) OnTimelineGap(roomID int, meta metrics.StreamObservationMeta, delta time.Duration) {
	o.svc.ObserveRecordingTimelineGap(recordingAlertContext(roomID, meta), delta)
}

func (o *streamNotifyObserver) OnSlowFlush(roomID int, meta metrics.StreamObservationMeta) {
	o.svc.ObserveRecordingSlowFlush(recordingAlertContext(roomID, meta))
}

func (o *streamNotifyObserver) OnSlowSync(roomID int, meta metrics.StreamObservationMeta) {
	o.svc.ObserveRecordingSlowSync(recordingAlertContext(roomID, meta))
}

func (o *streamNotifyObserver) OnEnqueueWait(roomID int, meta metrics.StreamObservationMeta, wait time.Duration) {
	o.svc.ObserveRecordingEnqueueWait(recordingAlertContext(roomID, meta), wait)
}

func recordingAlertContext(roomID int, meta metrics.StreamObservationMeta) RecordingAlertContext {
	return RecordingAlertContext{
		RoomID:    roomID,
		Streamer:  meta.Streamer,
		RoomTitle: meta.RoomTitle,
	}
}
