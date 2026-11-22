//go:build android

package bootstrap

import (
	"time"

	"go.uber.org/fx"
)

func NewAndroidApp() *fx.App {
	return fx.New(
		MainModule(),
		fx.StartTimeout(10*time.Second),
		fx.StopTimeout(10*time.Second),
		fx.WithLogger(fxZapLogger),
	)
}
