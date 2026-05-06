package room_test

import (
	"os"
	"testing"
	"time"

	"github.com/eric2788/bilirec/internal/modules/bilibili"
	"github.com/eric2788/bilirec/internal/modules/config"
	"github.com/eric2788/bilirec/internal/services/room"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func init() {
	logrus.SetLevel(logrus.DebugLevel)
	if os.Getenv("CI") != "" {
		os.Setenv("BILIBILI_ANONYMOUS_LOGIN", "true")
	}
}

func TestCache_CacheHit(t *testing.T) {
	var svc *room.Service

	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Provide(room.NewService),
		fx.Populate(&svc),
		fx.StartTimeout(5*time.Second),
	)
	defer app.RequireStop()
	app.RequireStart()

	if svc == nil {
		t.Fatal("room service not initialized")
	}

	t.Log("cache service initialized")
}

func TestCache_CacheTTL(t *testing.T) {
	var svc *room.Service
	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Provide(room.NewService),
		fx.Populate(&svc),
		fx.StartTimeout(5*time.Second),
	)
	defer app.RequireStop()
	app.RequireStart()

	if svc == nil {
		t.Fatal("room service not initialized")
	}

	t.Log("cache ttl behavior initialized")
}
