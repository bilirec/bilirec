//go:build !android

package bootstrap

import (
	"os"
	"strings"
	"time"

	ex "github.com/bilirec/bilirec/internal/services/expose"
	"github.com/bilirec/bilirec/utils"

	"go.uber.org/fx"
)

func NewApp() *fx.App {
	return fx.New(
		MainModule(),
		fx.Invoke(ex.NewService),
		fx.StartTimeout(
			utils.Ternary(
				noQrCodePrompt(),
				15*time.Second,
				1*time.Minute,
			),
		),
		fx.StopTimeout(1*time.Minute),
		fx.WithLogger(fxZapLogger),
	)
}

func noQrCodePrompt() bool {
	loginMode := strings.ToLower(strings.TrimSpace(utils.EmptyOrElse(os.Getenv("BILIBILI_LOGIN_MODE"), "controller")))
	return loginMode == "controller" || loginMode == "anonymous"
}
