//go:build cgo && android

package ffmpeg

import (
	"context"

	"github.com/sirupsen/logrus"
)

type FFmpegLogPayload struct {
	SessionID int64
	Level     int
	Message   string
}

var (
	logQueue  = make(chan *FFmpegLogPayload, 512)
	globalLog = logrus.WithField("component", "ffmpeg_core")
)

func init() {
	// 在 package 初始化時，自動啟動背景的日誌消費者 Goroutine
	go startLogConsumer()
	go activeSessions.StartCleanupJob(context.Background())
}

func ConsumeNativeLog(sessionID int64, level int, message string) {
	payload := &FFmpegLogPayload{
		SessionID: sessionID,
		Level:     level,
		Message:   message,
	}

	select {
	case logQueue <- payload:
	default:
		// 緩衝區滿時優雅降級，丟棄日誌，確保前線不塞車
	}
}

// startLogConsumer 是專職負責 Disk I/O 的背景執行緒
func startLogConsumer() {
	for payload := range logQueue {
		var log *logrus.Entry
		if taskLog, found := activeSessions.LoadStale(payload.SessionID); found && taskLog != nil {
			log = taskLog.WithField("component", "ffmpeg_core").WithField("session_id", payload.SessionID)
		} else {
			log = globalLog.WithField("session_id", payload.SessionID)
		}

		if payload.Level <= 16 {
			log.Errorf("[FFMPEG] %s", payload.Message)
		} else if payload.Level <= 24 {
			log.Warnf("[FFMPEG] %s", payload.Message)
		} else {
			log.Infof("[FFMPEG] %s", payload.Message)
		}
	}
}
