package stream

import (
	"testing"

	"github.com/bilirec/bilirec/internal/modules/config"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func initStreamTestConfig(tb testing.TB) *config.Config {
	tb.Helper()
	var cfg *config.Config
	app := fxtest.New(tb, config.Module, fx.Populate(&cfg))
	app.RequireStart()
	tb.Cleanup(app.RequireStop)
	return cfg
}
