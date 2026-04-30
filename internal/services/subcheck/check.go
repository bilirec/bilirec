package subcheck

import (
	"context"
	"maps"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/eric2788/bilirec/internal/modules/bilibili"
	"github.com/eric2788/bilirec/internal/modules/config"
	"github.com/eric2788/bilirec/internal/services/notify"
	"github.com/eric2788/bilirec/internal/services/recorder"
	"github.com/eric2788/bilirec/internal/services/room"
	"github.com/eric2788/bilirec/internal/services/subscribe"
	"github.com/eric2788/bilirec/pkg/db"
	"github.com/eric2788/bilirec/pkg/fp"
	"github.com/puzpuzpuz/xsync/v4"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

var logger = logrus.WithField("service", "subcheck")

const (
	checkInterval        = 1 * time.Minute
	liveStatesBucketName = "SubCheck_LiveStates"
)

type Service struct {
	subSvc     *subscribe.Service
	roomSvc    *room.Service
	recSvc     *recorder.Service
	notifySvc  *notify.Service
	bucket     *db.Bucket
	liveStates *xsync.Map[int, bool]

	ctx    context.Context
	cancel context.CancelFunc
}

func NewService(lc fx.Lifecycle, cfg *config.Config, subSvc *subscribe.Service, roomSvc *room.Service, recSvc *recorder.Service, notifySvc *notify.Service) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		subSvc:     subSvc,
		roomSvc:    roomSvc,
		recSvc:     recSvc,
		notifySvc:  notifySvc,
		liveStates: xsync.NewMap[int, bool](),
		ctx:        ctx,
		cancel:     cancel,
	}

	lc.Append(fx.StartStopHook(
		func() error { return s.start(cfg) },
		s.stop,
	))
	return s
}

func (s *Service) start(cfg *config.Config) error {
	client, err := db.Open(cfg.DatabaseDir + string(os.PathSeparator) + "subcheck.db")
	if err != nil {
		return err
	}
	bucket, err := client.Bucket(liveStatesBucketName)
	if err != nil {
		return err
	}
	s.bucket = bucket

	if err := bucket.ForEach(func(k, v []byte) error {
		roomID, err := strconv.Atoi(string(k))
		if err != nil {
			return nil // skip invalid keys
		}
		isLive := len(v) > 0 && v[0] == 1
		s.liveStates.Store(roomID, isLive)
		return nil
	}); err != nil {
		return err
	}

	go s.loop()
	return nil
}

func (s *Service) stop() error {
	s.cancel()
	return s.bucket.Close()
}

func (s *Service) loop() {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	s.tryStartAllAutoRecordRooms()
	for {
		select {
		case <-ticker.C:
			s.tryStartAllAutoRecordRooms()
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Service) tryStartAllAutoRecordRooms() {
	rooms, err := s.subSvc.ListSubscribedRoomsWithConfig()
	if err != nil {
		logger.Warnf("failed to list room subscriptions: %v", err)
		return
	}

	liveCheckRooms := fp.FilterByValue(rooms, func(cfg *subscribe.RoomConfig) bool {
		return cfg != nil && (cfg.Notify || cfg.AutoRecord)
	})

	notifyRoomInfos := s.getNotifyRoomInfos(slices.Collect(maps.Keys(liveCheckRooms)))

	s.invalidateNotified(rooms)

	for roomID, cfg := range liveCheckRooms {
		info, ok := notifyRoomInfos[roomID]
		if !ok || info == nil {
			continue
		}
		isLive := info.LiveStatus == 1

		logger.Debugf("checking room %d (%s): isLive=%v, config=%+v", roomID, info.Uname, isLive, cfg)

		if cfg.AutoRecord {
			status := s.recSvc.GetStatus(roomID)
			if status == recorder.Recording || status == recorder.Recovering {
				continue
			}

			if !isLive {
				s.clearNotified(roomID)
				continue
			}

			err := s.recSvc.Start(roomID)
			if err == nil {
				logger.Infof("started recording for room %d (%s) from auto-record", roomID, info.Uname)
				if cfg.Notify {
					s.publishLiveOnce(roomID, info.Uname, info.Title, true)
				}
				continue
			}

			switch err {
			case recorder.ErrRecordingStarted:
				continue
			default:
				logger.Warnf("failed to start recording for room %d (%s) from auto-record: %v", roomID, info.Uname, err)
				continue
			}
		} else if cfg.Notify {
			if isLive {
				s.publishLiveOnce(roomID, info.Uname, info.Title, false)
			} else {
				s.clearNotified(roomID)
			}
			continue
		}
	}
}

func (s *Service) clearNotified(roomID int) {
	wasLive, _ := s.liveStates.LoadAndStore(roomID, false)
	if !wasLive {
		return
	} else if err := s.bucket.Put([]byte(strconv.Itoa(roomID)), []byte{0}); err != nil {
		logger.Warnf("failed to update live state for room %d: %v", roomID, err)
	}
}

func (s *Service) publishLiveOnce(roomID int, streamer string, roomTitle string, autoRecordStarted bool) {
	wasLive, _ := s.liveStates.LoadAndStore(roomID, true)
	if wasLive {
		logger.Debugf("skipped notification for room %d (%s) due to notified.", roomID, streamer)
		return
	}
	s.notifySvc.PublishLive(roomID, streamer, roomTitle, autoRecordStarted)
	if err := s.bucket.Put([]byte(strconv.Itoa(roomID)), []byte{1}); err != nil {
		logger.Warnf("failed to update live state for room %d: %v", roomID, err)
	}
}

func (s *Service) invalidateNotified(rooms map[int]*subscribe.RoomConfig) {
	staleRooms := make([]int, 0)
	s.liveStates.Range(func(key int, value bool) bool {
		if cfg, ok := rooms[key]; !ok || cfg == nil || !cfg.Notify {
			staleRooms = append(staleRooms, key)
		}
		return true
	})
	for roomID := range staleRooms {
		if err := s.bucket.Delete([]byte(strconv.Itoa(roomID))); err != nil {
			logger.Warnf("failed to delete live state for room %d: %v", roomID, err)
		}
		s.liveStates.Delete(roomID)
	}
}

func (s *Service) getNotifyRoomInfos(liveCheckRoomIDs []int) map[int]*bilibili.LiveRoomInfoDetail {
	notifyRoomInfos := make(map[int]*bilibili.LiveRoomInfoDetail)

	if len(liveCheckRoomIDs) > 0 {
		infos, err := s.roomSvc.GetMultipleRoomInfos(liveCheckRoomIDs...)
		if err != nil {
			logger.Warnf("batch fetch room info failed: %v, fallback to per-room check", err)
			for _, roomID := range liveCheckRoomIDs {
				info, checkErr := s.roomSvc.GetLiveRoomInfo(roomID)
				if checkErr != nil {
					logger.Warnf("failed to get room info for room %d: %v", roomID, checkErr)
					continue
				}
				notifyRoomInfos[roomID] = info
			}
		} else {
			for _, roomID := range liveCheckRoomIDs {
				if info, ok := infos[strconv.Itoa(roomID)]; ok && info != nil {
					notifyRoomInfos[roomID] = info
				}
			}
		}
	}

	return notifyRoomInfos
}
