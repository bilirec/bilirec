package logging

import (
	"context"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/pkg/logger"
	logsink "github.com/bilirec/bilirec/pkg/sink"
	"go.uber.org/fx"
)

var log = logger.Named("logging")

func provider(lc fx.Lifecycle, cfg *config.Config, exporter *metrics.Exporter) {
	wireLocalLogger(lc, cfg)
	wireRemoteVLogs(lc, cfg, exporter)
}

// shutdownSink flushes pending logs, detaches the zap core, then stops the sink.
// Order: Sync → ClearCore → Stop, so no new logs reach the sink during shutdown
// and buffered data is flushed while the sink still accepts writes.
func shutdownSink(clearCore func(), sink *logsink.AsyncBufferedSink, ctx context.Context) error {
	_ = sink.Sync()
	clearCore()
	return sink.Stop(ctx)
}

var Module = fx.Module("logging", fx.Invoke(provider))
