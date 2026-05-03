package room

import (
	"time"

	"github.com/eric2788/bilirec/internal/modules/bilibili"
	"github.com/eric2788/bilirec/pkg/swr"
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
			svc.cache.Stop()
			svc.cache.DeleteAll()
			return nil
		},
	))

	return svc
}
