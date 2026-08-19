package subcheck

import (
	"context"
	"maps"
	"math/rand"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/internal/services/notify"
	"github.com/bilirec/bilirec/internal/services/recorder"
	"github.com/bilirec/bilirec/internal/services/room"
	"github.com/bilirec/bilirec/internal/services/subscribe"
	"github.com/bilirec/bilirec/pkg/coordinator"
	"github.com/bilirec/bilirec/pkg/db"
	"github.com/bilirec/bilirec/pkg/fp"
	"github.com/puzpuzpuz/xsync/v4"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

var logger = logrus.WithField("service", "subcheck")

const sessionKeysBucketName = "SubCheck_LiveStates"

type Service struct {
	subSvc      *subscribe.Service
	roomSvc     *room.Service
	recSvc      *recorder.Service
	notifySvc   *notify.Service
	m           *metrics.Exporter
	bucket      *db.Bucket
	sessionKeys *xsync.Map[int, string]
	coordinator *coordinator.RoundRobin
	shardCount  int
	shardStops  []func()

	checkInterval  time.Duration
	scheduleParams scheduleParams
	lastRescale    time.Time
	scheduleMu     sync.Mutex
	jitterSecs     int

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewService(lc fx.Lifecycle, cfg *config.Config, subSvc *subscribe.Service, roomSvc *room.Service, recSvc *recorder.Service, notifySvc *notify.Service, m *metrics.Exporter) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		subSvc:      subSvc,
		roomSvc:     roomSvc,
		recSvc:      recSvc,
		notifySvc:   notifySvc,
		m:           m,
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

	s.scheduleParams = scheduleParamsFromConfig(
		cfg.SubcheckRoomsPerShard,
		cfg.SubcheckTickSecs,
		cfg.SubcheckMinIntervalSecs,
		cfg.SubcheckMaxIntervalSecs,
		cfg.SubcheckMaxShards,
	)
	s.jitterSecs = cfg.SubcheckJitterSecs
	roomCount, err := s.countLiveCheckRooms()
	if err != nil {
		logger.Warnf("启动时统计订阅检查房间数失败：%v", err)
		roomCount = 0
	}
	sched := computeSchedule(roomCount, s.scheduleParams)
	s.shardCount = sched.shards
	s.checkInterval = sched.interval
	logger.Infof("subcheck 调度：rooms=%d shards=%d interval=%s", roomCount, sched.shards, sched.interval)

	s.coordinator = coordinator.NewRoundRobin(s.checkInterval)
	// Keep one shard tick responsive when shard count is large.
	s.coordinator.SetMinTick(time.Second)

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

	// Run one full check cycle at startup so behavior matches previous implementation.
	s.tryStartAllAutoRecordRooms()

	maxShards := s.scheduleParams.maxShards
	s.shardStops = make([]func(), 0, maxShards)
	for shard := 0; shard < maxShards; shard++ {
		ch, unregister := s.coordinator.Register(nil)
		s.shardStops = append(s.shardStops, unregister)
		s.wg.Add(1)
		go s.shardLoop(shard, ch)
	}

	<-s.ctx.Done()
	for _, stop := range s.shardStops {
		stop()
	}
	s.shardStops = nil
}

func (s *Service) shardLoop(shard int, ch <-chan struct{}) {
	defer s.wg.Done()
	for {
		select {
		case <-ch:
			if s.jitterSecs > 0 {
				time.Sleep(time.Duration(rand.Intn(s.jitterSecs)) * time.Second)
			}
			if shard == 0 {
				s.maybeRescale()
			}
			s.scheduleMu.Lock()
			activeShards := s.shardCount
			s.scheduleMu.Unlock()
			if shard >= activeShards {
				continue
			}
			s.tryStartShardAutoRecordRooms(shard, activeShards)
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Service) tryStartAllAutoRecordRooms() {
	s.tryStartShardAutoRecordRooms(0, 1)
}

func (s *Service) tryStartShardAutoRecordRooms(shardIndex, shardCount int) {
	if shardCount <= 0 {
		shardCount = 1
	}

	rooms, err := s.subSvc.ListSubscribedRoomsWithConfig()
	if err != nil {
		logger.Warnf("列出房间订阅失败：%v", err)
		return
	}

	liveCheckRooms := fp.FilterByValue(rooms, func(cfg *subscribe.RoomConfig) bool {
		return cfg != nil && (cfg.Notify || cfg.AutoRecord)
	})
	if shardCount > 1 {
		liveCheckRooms = fp.FilterByKey(liveCheckRooms, func(roomID int) bool {
			return roomID%shardCount == shardIndex
		})
	}

	liveCheckRoomIDs := slices.Collect(maps.Keys(liveCheckRooms))
	notifyRoomInfos := s.getNotifyRoomInfos(liveCheckRoomIDs)

	// Stale room cleanup only needs one shard per cycle.
	if shardIndex == 0 {
		s.invalidateStaleRooms(rooms)
	}

	for roomID, cfg := range liveCheckRooms {
		info, ok := notifyRoomInfos[roomID]
		if !ok || info == nil {
			continue
		}
		isLive := info.LiveStatus == 1
		currentSessionKey := resolveLiveSessionKey(info)

		// 每輪無條件更新 live_status gauge（自愈設計：重啟後不需依賴開播事件），順帶更新 room_info
		s.m.SetLiveStatus(roomID, info.Uname, isLive)

		if !isLive || currentSessionKey == "" {
			s.clearSessionState(roomID)
			continue
		}

		storedSessionKey, loaded := s.sessionKeys.Load(roomID)
		if loaded && storedSessionKey == currentSessionKey {
			continue
		}

		logger.Debugf("new live session detected for room %d (%s), key: %s", roomID, info.Uname, currentSessionKey)
		s.m.LiveSessionDetected(roomID)
		state := notify.LiveStateLiveDetected

		if cfg.AutoRecord {
			status := s.recSvc.GetStatus(roomID)
			if status != recorder.Recording && status != recorder.Recovering {
				// Resolve duration from subscription config: -1 = unlimited, >0 = custom minutes.
				var autoRecordArgs []recorder.RecordStartOption
				switch {
				case cfg.RecordDurationMinutes == -1:
					autoRecordArgs = append(autoRecordArgs, recorder.WithDuration(0))
				case cfg.RecordDurationMinutes > 0:
					autoRecordArgs = append(autoRecordArgs, recorder.WithDuration(time.Duration(cfg.RecordDurationMinutes)*time.Minute))
				}

				streamOptions := streamOptionsFromRoomConfig(cfg)
				if len(streamOptions) > 0 {
					autoRecordArgs = append(autoRecordArgs, recorder.WithStreamOptions(streamOptions...))
				}
				if cfg.RecordDanmaku {
					autoRecordArgs = append(autoRecordArgs, recorder.WithRecordDanmaku(true))
				}

				err := s.recSvc.Start(roomID, autoRecordArgs...)
				switch err {
				case nil, recorder.ErrRecordingStarted, recorder.ErrRecordRecovering, recorder.ErrRecordingPending:
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

func streamOptionsFromRoomConfig(cfg *subscribe.RoomConfig) []bilibili.GetStreamURLsOption {
	if cfg == nil {
		return nil
	}

	var opts []bilibili.GetStreamURLsOption
	if cfg.Qn > 0 {
		qn := bilibili.Quality(cfg.Qn)
		if qn.IsValid() {
			opts = append(opts, bilibili.WithQn(qn))
		}
	}
	if cfg.OnlyAudio {
		opts = append(opts, bilibili.WithOnlyAudio(true))
	}
	if profiles, err := bilibili.NormalizeStreamProfiles(cfg.StreamProfiles); err == nil && len(profiles) > 0 {
		opts = append(opts, bilibili.WithProfiles(profiles...))
	}
	return opts
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
		s.m.UnregisterLiveRoom(roomID)
		logger.Debugf("removed stale session state for room: %v", roomID)
	}
}

func (s *Service) getNotifyRoomInfos(liveCheckRoomIDs []int) map[int]*bilibili.LiveRoomInfoDetail {
	notifyRoomInfos := make(map[int]*bilibili.LiveRoomInfoDetail)

	if len(liveCheckRoomIDs) > 0 {
		infos, err := s.roomSvc.RefreshRoomInfos(liveCheckRoomIDs...)
		if err != nil {
			logger.Warnf("强制刷新房间信息失败：%v，回退到逐房间检查", err)
			for _, roomID := range liveCheckRoomIDs {
				one, checkErr := s.roomSvc.RefreshRoomInfos(roomID)
				if checkErr != nil {
					logger.Warnf("获取房间 %d 信息失败：%v", roomID, checkErr)
					continue
				}
				if info, ok := one[strconv.Itoa(roomID)]; ok {
					notifyRoomInfos[roomID] = info
				}
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
