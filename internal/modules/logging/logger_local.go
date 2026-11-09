package logging

import (
	"context"
	"io"
	"path/filepath"
	"strings"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/logger"
	logsink "github.com/bilirec/bilirec/pkg/sink"
	"go.uber.org/fx"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// levelEnabler delegates level checks to pkg/logger's dynamic level.
// This ensures cores built here respect runtime level changes made via logger.SetLevel.
type levelEnabler struct{}

func (levelEnabler) Enabled(l zapcore.Level) bool {
	return l >= logger.Level()
}

func wireLocalLogger(lc fx.Lifecycle, cfg *config.Config) {
	if !cfg.LocalLogsEnabled {
		return
	}

	logPath := strings.TrimSpace(cfg.LocalLogsPath)
	if logPath == "" {
		logPath = filepath.Join(cfg.DatabaseDir, "bilirec.log")
	}

	// Build lumberjack writer
	fileWriter := &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    cfg.LocalLogsMaxSizeMB,
		MaxAge:     cfg.LocalLogsMaxAgeDays,
		MaxBackups: cfg.LocalLogsMaxBackups,
		Compress:   cfg.LocalLogsCompress,
	}

	tr := logsink.NewFileTransport(crashFollowWriter{path: logPath, w: fileWriter})
	overflow := logsink.OverflowDrop
	if strings.ToLower(strings.TrimSpace(cfg.LocalLogsOverflow)) == "block" {
		overflow = logsink.OverflowBlock
	}
	bufferedSink, err := logsink.NewAsyncBufferedSink(tr, logsink.Options{
		BufferSize:    cfg.LocalLogsBufferSize,
		BatchBytes:    cfg.LocalLogsBatchBytes,
		FlushInterval: cfg.LocalLogsFlushInterval,
		Overflow:      overflow,
	})
	if err != nil {
		log.Warnf("初始化本地日志失败，已跳过：%v", err)
		return
	}

	// Choose encoder according to config
	var enc zapcore.Encoder
	switch strings.ToLower(strings.TrimSpace(cfg.LocalLogsFormat)) {
	case "jsonline":
		enc = logger.NewJsonLineEncoder()
	default:
		enc = logger.NewPrettyEncoder(false)
	}

	localCore := zapcore.NewCore(enc, bufferedSink, levelEnabler{})
	logger.SetLocalCore(localCore)

	if err := logger.EnableCrashCapture(logPath); err != nil {
		log.Warnf("启用崩溃输出到本地日志失败：%v", err)
	}

	lc.Append(fx.StopHook(func(ctx context.Context) error {
		logger.DisableCrashCapture()
		return shutdownSink(logger.ClearLocalCore, bufferedSink, ctx)
	}))

	log.Infof("本地日志已启用：%s format=%s", logPath, cfg.LocalLogsFormat)
}

// crashFollowWriter 在每次批次写入后让崩溃转储跟随当前日志文件。
// lumberjack 按大小换档后，runtime 仍持有旧 inode 的 fd；这里发现路径指向
// 新文件时重新注册（同一文件时是廉价的 SameFile 检查）。
type crashFollowWriter struct {
	path string
	w    io.WriteCloser
}

func (f crashFollowWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	logger.NoteCrashLogWrite(f.path)
	return n, err
}

func (f crashFollowWriter) Close() error { return f.w.Close() }
