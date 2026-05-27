package subcheck

import (
	"context"
	"maps"
	"os"
	"slices"
	"strconv"
	"sync"
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
	checkInterval         = 1 * time.Minute
	sessionKeysBucketName = "SubCheck_LiveStates"
)

type Service struct {
	subSvc      *subscribe.Service
	roomSvc     *room.Service
	recSvc      *recorder.Service
	notifySvc   *notify.Service
	bucket      *db.Bucket
	sessionKeys *xsync.Map[int, string]

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewService(lc fx.Lifecycle, cfg *config.Config, subSvc *subscribe.Service, roomSvc *room.Service, recSvc *recorder.Service, notifySvc *notify.Service) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		subSvc:      subSvc,
		roomSvc:     roomSvc,
		recSvc:      recSvc,
		notifySvc:   notifySvc,
		sessionKeys: xsync.NewMap[int, string](),
		ctx:         ctx,
		cancel:      cancel,
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
	bucket, err := client.Bucket(sessionKeysBucketName)
	if err != nil {
		return err
	}
	s.bucket = bucket

	if err := bucket.ForEach(func(k, v []byte) error {
		roomID, err := strconv.Atoi(string(k))
		if err != nil {
			return nil // skip invalid keys
		}
		if len(v) == 0 {
			return nil
		}
		// Backward compatibility: historical format was bool-like [0]/[1].
		if len(v) == 1 && (v[0] == 0 || v[0] == 1) {
			return nil
		}
		s.sessionKeys.Store(roomID, string(v))
		return nil
	}); err != nil {
		return err
	}

	s.wg.Add(1)
	go s.loop()
	return nil
}

func (s *Service) stop() error {
	s.cancel()
	s.wg.Wait()
	return s.bucket.Close()
}

func (s *Service) loop() {
	defer s.wg.Done()
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
		logger.Warnf("列出房间订阅失败：%v", err)
		return
	}

	liveCheckRooms := fp.FilterByValue(rooms, func(cfg *subscribe.RoomConfig) bool {
		return cfg != nil && (cfg.Notify || cfg.AutoRecord)
	})

	liveCheckRoomIDs := slices.Collect(maps.Keys(liveCheckRooms))
	s.roomSvc.InvalidateRooms(liveCheckRoomIDs...)
	notifyRoomInfos := s.getNotifyRoomInfos(liveCheckRoomIDs)

	s.invalidateStaleRooms(rooms)

	for roomID, cfg := range liveCheckRooms {
		info, ok := notifyRoomInfos[roomID]
		if !ok || info == nil {
			continue
		}
		isLive := info.LiveStatus == 1
		currentSessionKey := resolveLiveSessionKey(info)

		if !isLive || currentSessionKey == "" {
			s.clearSessionState(roomID)
			continue
		}

		storedSessionKey, loaded := s.sessionKeys.Load(roomID)
		if loaded && storedSessionKey == currentSessionKey {
			continue
		}

		logger.Debugf("new live session detected for room %d (%s), key: %s", roomID, info.Uname, currentSessionKey)
		state := notify.LiveStateLiveDetected

		if cfg.AutoRecord {
			status := s.recSvc.GetStatus(roomID)
			if status != recorder.Recording && status != recorder.Recovering {
				// Resolve duration from subscription config: -1 = unlimited, >0 = custom minutes.
				var autoRecordArgs []recorder.RecordStartOption
				switch {
				case cfg.RecordDurationMinutes == -1:
					autoRecordArgs = []recorder.RecordStartOption{recorder.WithDuration(0)}
				case cfg.RecordDurationMinutes > 0:
					autoRecordArgs = []recorder.RecordStartOption{recorder.WithDuration(time.Duration(cfg.RecordDurationMinutes) * time.Minute)}
				}

				err := s.recSvc.Start(roomID, autoRecordArgs...)
				switch err {
				case nil, recorder.ErrRecordingStarted:
					state = notify.LiveStateAutoRecordStarted
					logger.Infof("已开始录制房间 %d（%s）", roomID, info.Uname)
				default:
					state = notify.LiveStateAutoRecordFailed
					logger.Warnf("开始录制房间 %d 失败：%v", roomID, err)
				}
			}
		}

		if cfg.Notify {
			s.notifySvc.PublishLiveState(roomID, info.Uname, info.Title, state)
		}

		s.markSessionState(roomID, currentSessionKey)
	}
}

func (s *Service) markSessionState(roomID int, sessionKey string) {
	s.sessionKeys.Store(roomID, sessionKey)
	if err := s.bucket.Put([]byte(strconv.Itoa(roomID)), []byte(sessionKey)); err != nil {
		logger.Warnf("保存房间 %d 会话密钥失败：%v", roomID, err)
	}
}

func (s *Service) clearSessionState(roomID int) {
	_, loaded := s.sessionKeys.LoadAndDelete(roomID)
	if !loaded {
		return
	}
	if err := s.bucket.Delete([]byte(strconv.Itoa(roomID))); err != nil {
		logger.Warnf("清理房间 %d 会话状态失败：%v", roomID, err)
	}
}

func (s *Service) invalidateStaleRooms(rooms map[int]*subscribe.RoomConfig) {
	staleRooms := make([]int, 0)
	s.sessionKeys.Range(func(key int, value string) bool {
		if cfg, ok := rooms[key]; !ok || cfg == nil || (!cfg.Notify && !cfg.AutoRecord) {
			staleRooms = append(staleRooms, key)
		}
		return true
	})
	for _, roomID := range staleRooms {
		s.clearSessionState(roomID)
		logger.Debugf("removed stale session state for room: %v", roomID)
	}
}

func (s *Service) getNotifyRoomInfos(liveCheckRoomIDs []int) map[int]*bilibili.LiveRoomInfoDetail {
	notifyRoomInfos := make(map[int]*bilibili.LiveRoomInfoDetail)

	if len(liveCheckRoomIDs) > 0 {
		infos, err := s.roomSvc.GetMultipleRoomInfos(liveCheckRoomIDs...)
		if err != nil {
			logger.Warnf("批量获取房间信息失败：%v，回退到逐房间检查", err)
			for _, roomID := range liveCheckRoomIDs {
				info, checkErr := s.roomSvc.GetLiveRoomInfo(roomID)
				if checkErr != nil {
					logger.Warnf("获取房间 %d 信息失败：%v", roomID, checkErr)
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

func resolveLiveSessionKey(info *bilibili.LiveRoomInfoDetail) string {
	if info == nil {
		return ""
	}
	if info.LiveIDStr != "" && info.LiveIDStr != "0" {
		return "live_id_str:" + info.LiveIDStr
	}
	if info.LiveID > 0 {
		return "live_id:" + strconv.FormatInt(info.LiveID, 10)
	}
	if info.LiveTime != "" && info.LiveTime != "0000-00-00 00:00:00" {
		return "live_time:" + info.LiveTime
	}
	return ""
}
