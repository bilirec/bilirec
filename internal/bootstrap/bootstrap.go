package bootstrap

import (
	"os"
	"strings"
	"time"

	"github.com/eric2788/bilirec/internal/controllers/auth"
	"github.com/eric2788/bilirec/internal/controllers/convert"
	"github.com/eric2788/bilirec/internal/controllers/file"
	nc "github.com/eric2788/bilirec/internal/controllers/notify"
	"github.com/eric2788/bilirec/internal/controllers/record"
	"github.com/eric2788/bilirec/internal/controllers/room"
	"github.com/eric2788/bilirec/internal/modules/bilibili"
	"github.com/eric2788/bilirec/internal/modules/config"
	"github.com/eric2788/bilirec/internal/modules/rest"
	co "github.com/eric2788/bilirec/internal/services/convert"
	ex "github.com/eric2788/bilirec/internal/services/expose"
	fi "github.com/eric2788/bilirec/internal/services/file"
	no "github.com/eric2788/bilirec/internal/services/notify"
	pa "github.com/eric2788/bilirec/internal/services/path"
	re "github.com/eric2788/bilirec/internal/services/recorder"
	ro "github.com/eric2788/bilirec/internal/services/room"
	st "github.com/eric2788/bilirec/internal/services/stream"
	sc "github.com/eric2788/bilirec/internal/services/subcheck"
	su "github.com/eric2788/bilirec/internal/services/subscribe"
	"github.com/eric2788/bilirec/utils"
	"go.uber.org/fx"
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

		fx.Invoke(ex.NewService),
		fx.Invoke(sc.NewService),

		fx.Invoke(room.NewController),
		fx.Invoke(nc.NewController),
		fx.Invoke(record.NewController),
		fx.Invoke(file.NewController),
		fx.Invoke(auth.NewController),
		fx.Invoke(convert.NewController),
	)
}

func noQrCodePrompt() bool {
	loginMode := strings.ToLower(strings.TrimSpace(utils.EmptyOrElse(os.Getenv("BILIBILI_LOGIN_MODE"), "startup")))
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
	)
}
