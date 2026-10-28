package stream

import (
	"os"
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

func TestMain(m *testing.M) {
	// Some stream tests may rely on defaults; harmless for connect-only package.
	_ = os.Setenv("SKIP_SMALL_FLUSH", "true")
	_ = os.Setenv("SKIP_SMALL_FLUSH_THRESHOLD", "1024")
	os.Exit(m.Run())
}
