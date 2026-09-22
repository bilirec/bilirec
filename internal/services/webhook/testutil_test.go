package webhook

import (
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func newWebhookService(t *testing.T, cfg *config.Config) *Service {
	t.Helper()
	var svc *Service
	app := fxtest.New(t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(NewService),
		fx.Populate(&svc),
		fx.StartTimeout(10*time.Second),
		fx.StopTimeout(10*time.Second),
	)
	app.RequireStart()
	t.Cleanup(func() {
		app.RequireStop()
	})
	return svc
}
