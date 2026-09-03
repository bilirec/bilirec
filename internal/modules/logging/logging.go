package logging

import (
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/pkg/logger"
	"go.uber.org/fx"
)

var log = logger.Named("logging")

func provider(lc fx.Lifecycle, cfg *config.Config, exporter *metrics.Exporter) {
	wireLocalLogger(lc, cfg)
	wireRemoteVLogs(lc, cfg, exporter)
}

var Module = fx.Module("logging", fx.Invoke(provider))
