package bootstrap

import (
	"os"
	"strings"
	"time"

	"github.com/bilirec/bilirec/internal/controllers/auth"
	"github.com/bilirec/bilirec/internal/controllers/convert"
	"github.com/bilirec/bilirec/internal/controllers/file"
	nc "github.com/bilirec/bilirec/internal/controllers/notify"
	"github.com/bilirec/bilirec/internal/controllers/record"
	"github.com/bilirec/bilirec/internal/controllers/room"
	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/rest"
	co "github.com/bilirec/bilirec/internal/services/convert"
	ex "github.com/bilirec/bilirec/internal/services/expose"
	fi "github.com/bilirec/bilirec/internal/services/file"
	ja "github.com/bilirec/bilirec/internal/services/janitor"
	no "github.com/bilirec/bilirec/internal/services/notify"
	pa "github.com/bilirec/bilirec/internal/services/path"
	re "github.com/bilirec/bilirec/internal/services/recorder"
	ro "github.com/bilirec/bilirec/internal/services/room"
	st "github.com/bilirec/bilirec/internal/services/stream"
	sc "github.com/bilirec/bilirec/internal/services/subcheck"
	su "github.com/bilirec/bilirec/internal/services/subscribe"
	"github.com/bilirec/bilirec/pkg/updatecheck"
	"github.com/bilirec/bilirec/utils"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func MainModule() fx.Option {
	return fx.Module("main",
		config.Module,
		bilibili.Module,
		rest.Module,

		fx.Provide(pa.NewService),
		fx.Provide(co.NewService),
		fx.Provide(st.NewService),
		fx.Provide(re.NewService),
		fx.Provide(ro.NewService),
		fx.Provide(su.NewService),
		fx.Provide(no.NewService),
		fx.Provide(fi.NewService),

		fx.Invoke(ja.NewService),
		fx.Invoke(ex.NewService),
		fx.Invoke(sc.NewService),

		fx.Invoke(updatecheck.InvokeCheck),

		fx.Invoke(room.NewController),
		fx.Invoke(nc.NewController),
		fx.Invoke(record.NewController),
		fx.Invoke(file.NewController),
		fx.Invoke(auth.NewController),
		fx.Invoke(convert.NewController),
	)
}

func noQrCodePrompt() bool {
	loginMode := strings.ToLower(strings.TrimSpace(utils.EmptyOrElse(os.Getenv("BILIBILI_LOGIN_MODE"), "controller")))
	return loginMode == "controller" || loginMode == "anonymous"
}

func NewApp() *fx.App {
	return fx.New(
		MainModule(),
		fx.StartTimeout(
			utils.Ternary(
				noQrCodePrompt(),
				15*time.Second,
				1*time.Minute,
			),
		),
		fx.StopTimeout(1*time.Minute),
		fx.Provide(zapLogger),
		fx.WithLogger(func(log *zap.Logger) fxevent.Logger {
			return &fxevent.ZapLogger{Logger: log}
		}),
	)
}

func NewAndroidApp() *fx.App {
	return fx.New(
		MainModule(),
		fx.StartTimeout(10*time.Second),
		fx.StopTimeout(10*time.Second),
		fx.Provide(zapLogger),
		fx.WithLogger(func(log *zap.Logger) fxevent.Logger {
			return &fxevent.ZapLogger{Logger: log}
		}),
	)
}

func zapLogger() (*zap.Logger, error) {
	cfg := zap.NewDevelopmentConfig()

	cfg.Level = zap.NewAtomicLevelAt(zapcore.WarnLevel)

	if os.Getenv("DEBUG") == "true" {
		cfg.Level = zap.NewAtomicLevelAt(zapcore.DebugLevel)
	}

	return cfg.Build()
}
