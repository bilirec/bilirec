package room

import (
	"time"

	"github.com/eric2788/bilirec/internal/modules/bilibili"
	"github.com/jellydator/ttlcache/v3"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

const defaultTTL = 5 * time.Minute

var logger = logrus.WithField("service", "room")

type Service struct {
	bilic *bilibili.Client
	cache *ttlcache.Cache[string, *bilibili.LiveRoomInfoDetail]
}

func NewService(lc fx.Lifecycle, bilic *bilibili.Client) *Service {
	svc := &Service{
		bilic: bilic,
		cache: ttlcache.New(
			ttlcache.WithTTL[string, *bilibili.LiveRoomInfoDetail](defaultTTL),
			ttlcache.WithCapacity[string, *bilibili.LiveRoomInfoDetail](100),
			ttlcache.WithDisableTouchOnHit[string, *bilibili.LiveRoomInfoDetail](),
		),
	}

	lc.Append(fx.StartStopHook(
		func() error {
			go svc.cache.Start()
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
