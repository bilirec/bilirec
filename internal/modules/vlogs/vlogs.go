package vlogs

import (
	"context"
	"os"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/pkg/logger"
	"go.uber.org/fx"
)

var log = logger.Named("vlogs")

func provider(lc fx.Lifecycle, cfg *config.Config, exporter *metrics.Exporter) {
	metrics := newModuleMetrics(exporter, cfg.VictoriaLogsEnabled && cfg.VictoriaLogsURL != "")
	if !cfg.VictoriaLogsEnabled {
		return
	}
	if cfg.VictoriaLogsURL == "" {
		log.Warn("VICTORIALOGS_ENABLED=true 但未设置 VICTORIALOGS_URL，远程日志已跳过")
		return
	}
	instance, _ := os.Hostname()
	sink := logger.AttachJSONLine(logger.JSONLineOptions{
		URL:          cfg.VictoriaLogsURL,
		StreamFields: cfg.VictoriaLogsStreamFields,
		Instance:     instance,
		AccountID:    cfg.VictoriaLogsAccountID,
		ProjectID:    cfg.VictoriaLogsProjectID,
		Timeout:      cfg.VictoriaLogsTimeout,
		RetryMax:     cfg.VictoriaLogsRetryMax,
		OnQueueBytes: metrics.addQueueBytes,
		OnDropped:    metrics.logDropped,
		OnFailed:     metrics.requestFailed,
		OnRetry:      metrics.retry,
	})

	lc.Append(fx.StopHook(func(ctx context.Context) error {
		return sink.Stop(ctx)
	}))

	log.Infof("VictoriaLogs 远程日志已启用：%s", cfg.VictoriaLogsURL)
}

var Module = fx.Module("vlogs", fx.Invoke(provider))
