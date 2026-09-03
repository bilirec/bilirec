package logging

import (
	"context"
	"net/url"
	"os"
	"strings"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/pkg/logger"
	logsink "github.com/bilirec/bilirec/pkg/sink"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// insertURL builds the VictoriaLogs /insert/jsonline endpoint URL.
// The _msg_field/_time_field values must stay in sync with NewVLogsEncoder's
// MessageKey (_msg) and TimeKey (_time).
func insertURL(base, streamFields string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return ""
	}
	u, err := url.Parse(base + "/insert/jsonline")
	if err != nil {
		return base + "/insert/jsonline?_stream_fields=" + url.QueryEscape(streamFields) +
			"&_msg_field=_msg&_time_field=_time"
	}
	q := u.Query()
	q.Set("_stream_fields", streamFields)
	q.Set("_msg_field", "_msg")
	q.Set("_time_field", "_time")
	u.RawQuery = q.Encode()
	return u.String()
}

func wireRemoteVLogs(lc fx.Lifecycle, cfg *config.Config, exporter *metrics.Exporter) {
	moduleMetrics := newModuleMetrics(exporter, cfg.VictoriaLogsEnabled && cfg.VictoriaLogsURL != "")
	if !cfg.VictoriaLogsEnabled {
		return
	}

	if cfg.VictoriaLogsURL == "" {
		log.Warn("VICTORIALOGS_ENABLED=true 但未设置 VICTORIALOGS_URL，远程日志已跳过")
		return
	}

	endpoint := insertURL(cfg.VictoriaLogsURL, cfg.VictoriaLogsStreamFields)
	if endpoint == "" {
		log.Warn("victorialogs insert URL invalid, skipping")
		return
	}

	instance, _ := os.Hostname()

	// Build VLogs HTTP transport
	tr := logsink.NewVLogsHTTPTransport(logsink.VLogsHTTPTransportOptions{
		URL:       endpoint,
		AccountID: cfg.VictoriaLogsAccountID,
		ProjectID: cfg.VictoriaLogsProjectID,
		Timeout:   cfg.VictoriaLogsTimeout,
		RetryMax:  cfg.VictoriaLogsRetryMax,
		OnRetry:   moduleMetrics.retry,
	})

	// Build buffered sink for remote
	bufferedSink, err := logsink.NewAsyncBufferedSink(tr, logsink.Options{
		// defaults will be used when zero
		Overflow: logsink.OverflowDrop,
		Hooks: logsink.Hooks{
			OnQueueBytes: moduleMetrics.addQueueBytes,
			OnDropped:    moduleMetrics.logDropped,
			OnFailed: func(err error) {
				moduleMetrics.requestFailed()
			},
		},
	})
	if err != nil {
		log.Warnf("初始化远程日志失败，已跳过：%v", err)
		return
	}

	enc := logger.NewVLogsEncoder()
	remoteCore := zapcore.
		NewCore(enc, bufferedSink, levelEnabler{}).
		With([]zapcore.Field{
			zap.String("app", "bilirec"),
			zap.String("instance", instance),
		})

	logger.SetRemoteCore(remoteCore)
	lc.Append(fx.StopHook(func(ctx context.Context) error {
		return shutdownSink(logger.ClearRemoteCore, bufferedSink, ctx)
	}))

	log.Infof("VictoriaLogs 远程日志已启用：%s", cfg.VictoriaLogsURL)
}
