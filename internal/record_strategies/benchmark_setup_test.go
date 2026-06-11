package record_strategies

import (
	"context"
	"os"
	"sync"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

var benchmarkConfigOnce sync.Once

// ensureBenchmarkConfig initializes global config for strategy pipeline benchmarks.
// Strategy BuildPipeline reads config.ReadOnly, which is normally set by fx at app start.
func ensureBenchmarkConfig() {
	benchmarkConfigOnce.Do(func() {
		if config.ReadOnly != nil {
			return
		}
		// Benchmarks measure full write path; disable SD-card deferred file creation.
		_ = os.Setenv("SKIP_SMALL_FLUSH", "false")
		logrus.SetLevel(logrus.ErrorLevel)
		app := fx.New(
			config.Module,
			fx.NopLogger,
			fx.Invoke(func(*config.Config) {}),
		)
		if err := app.Start(context.Background()); err != nil {
			panic(err)
		}
		if config.ReadOnly == nil {
			panic("config.ReadOnly not initialized for benchmarks")
		}
	})
}
