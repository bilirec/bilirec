package room

import (
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/pkg/swr"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

const (
	roomInfoSoftTTL = 5 * time.Minute
	roomInfoHardTTL = 30 * time.Minute
)

var logger = logrus.WithField("service", "room")

type Service struct {
	bilic *bilibili.Client
	cache *swr.Cache[string, *bilibili.LiveRoomInfoDetail]
}

func NewService(lc fx.Lifecycle, bilic *bilibili.Client) *Service {
	svc := &Service{
		bilic: bilic,
		cache: swr.NewCache[string, *bilibili.LiveRoomInfoDetail](roomInfoSoftTTL, roomInfoHardTTL, 2048),
	}

	lc.Append(fx.StartStopHook(
		func() error {
			svc.cache.Start()
			return nil
		},
		func() error {
			svc.cache.DeleteAll()
			svc.cache.Stop()
			return nil
		},
	))

	return svc
}
