package subcheck

import (
	"context"
	"errors"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/internal/services/notify"
	"github.com/bilirec/bilirec/internal/services/recorder"
	"github.com/bilirec/bilirec/internal/services/room"
	"github.com/bilirec/bilirec/internal/services/webhook"
	"github.com/bilirec/bilirec/internal/services/subscribe"
	"github.com/bilirec/bilirec/pkg/coordinator"
	"github.com/bilirec/bilirec/pkg/db"
	"github.com/bilirec/bilirec/pkg/fp"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/puzpuzpuz/xsync/v4"
	"go.uber.org/fx"
)

var log = logger.Named("subcheck")

const (
	sessionKeysBucketName = "SubCheck_LiveStates"
	maxAutoStartAttempts  = 5
)

type autoStartRetry struct {
	sessionKey string
	attempts   int
}

type Service struct {
	subSvc         *subscribe.Service
	roomSvc        *room.Service
	recSvc         *recorder.Service
	notifySvc *notify.Service
	wh        *webhook.Service
	webhookOn bool
	m         *metrics.Exporter
	bucket         *db.Bucket
	sessionKeys    *xsync.Map[int, string]
	autoStartRetry *xsync.Map[int, autoStartRetry]
	coordinator    *coordinator.RoundRobin
	shardCount     int
	shardStops     []func()

	checkInterval  time.Duration
	scheduleParams scheduleParams
	lastRescale    time.Time
	scheduleMu     sync.Mutex
	jitterSecs     int

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewService(lc fx.Lifecycle, cfg *config.Config, subSvc *subscribe.Service, roomSvc *room.Service, recSvc *recorder.Service, notifySvc *notify.Service, m *metrics.Exporter, webhookSvc *webhook.Service) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		subSvc:         subSvc,
		roomSvc:        roomSvc,
		recSvc:         recSvc,
		notifySvc:      notifySvc,
		webhookOn: cfg.WebhookConfigured(),
		m:         m,
		sessionKeys:    xsync.NewMap[int, string](),
		autoStartRetry: xsync.NewMap[int, autoStartRetry](),
		ctx:            ctx,
		cancel: cancel,
	}
	if cfg.WebhookConfigured() {
		s.wh = webhookSvc
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
		log.Warnf("启动时统计订阅检查房间数失败：%v", err)
		roomCount = 0
	}
	sched := computeSchedule(roomCount, s.scheduleParams)
	s.shardCount = sched.shards
	s.checkInterval = sched.interval
	log.Infof("subcheck 调度：rooms=%d shards=%d interval=%s", roomCount, sched.shards, sched.interval)

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

func needsLiveAction(cfg *subscribe.RoomConfig) bool {
	return cfg != nil && (cfg.Notify || cfg.AutoRecord)
}

func partitionShardRooms(rooms map[int]*subscribe.RoomConfig, shardIndex, shardCount int) (flaggedIDs, cachedIDs []int, shardRooms map[int]*subscribe.RoomConfig) {
	shardRooms = rooms
	if shardCount > 1 {
		shardRooms = fp.FilterByKey(rooms, func(roomID int) bool {
			return roomID%shardCount == shardIndex
		})
	}

	for roomID, cfg := range shardRooms {
		if needsLiveAction(cfg) {
			flaggedIDs = append(flaggedIDs, roomID)
		} else if cfg != nil {
			cachedIDs = append(cachedIDs, roomID)
		}
	}
	return flaggedIDs, cachedIDs, shardRooms
}

func (s *Service) tryStartShardAutoRecordRooms(shardIndex, shardCount int) {
	if shardCount <= 0 {
		shardCount = 1
	}

	rooms, err := s.subSvc.ListSubscribedRoomsWithConfig()
	if err != nil {
		log.Warnf("列出房间订阅失败：%v", err)
		return
	}

	flaggedIDs, cachedIDs, shardRooms := partitionShardRooms(rooms, shardIndex, shardCount)
	roomInfos := s.getNotifyRoomInfos(flaggedIDs)
	for roomID, info := range s.getCachedRoomInfos(cachedIDs) {
		roomInfos[roomID] = info
	}

	// Stale room cleanup only needs one shard per cycle.
	if shardIndex == 0 {
		s.invalidateStaleRooms(rooms)
	}

	for roomID, cfg := range shardRooms {
		info, ok := roomInfos[roomID]
		if !ok || info == nil {
			continue
		}
		isLive := info.LiveStatus == 1
		currentSessionKey := resolveLiveSessionKey(info)

		// 每輪無條件更新 live_status gauge（自愈設計：重啟後不需依賴開播事件），順帶更新 room_info
		s.m.SetLiveStatus(roomID, info.Uname, isLive)

		if !isLive || currentSessionKey == "" {
			if s.clearSessionState(roomID) {
				s.emitStreamEnded(info)
			}
			continue
		}

		storedSessionKey, loaded := s.sessionKeys.Load(roomID)
		if loaded && storedSessionKey == currentSessionKey {
			continue
		}

		retry, isRetry := s.autoStartRetry.Load(roomID)
		isRetry = isRetry && retry.sessionKey == currentSessionKey
		if !isRetry {
			log.Debugf("new live session detected for room %d (%s), key: %s", roomID, info.Uname, currentSessionKey)
			s.m.LiveSessionDetected(roomID)
			s.emitStreamStarted(info)
		}

		state := notify.LiveStateLiveDetected
		markNow := true

		if cfg != nil && cfg.AutoRecord {
			status := s.recSvc.GetStatus(roomID)
			if status != recorder.Recording && status != recorder.Recovering {
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
					if isRetry {
						state = notify.LiveStateAutoRecordRetryStarted
					} else {
						state = notify.LiveStateAutoRecordStarted
					}
					log.Infof("已开始录制房间 %d（%s）", roomID, info.Uname)
				default:
					attempts := 1
					if isRetry {
						attempts = retry.attempts + 1
					}
					if isTransientAutoStartError(err) && attempts < maxAutoStartAttempts {
						s.autoStartRetry.Store(roomID, autoStartRetry{sessionKey: currentSessionKey, attempts: attempts})
						markNow = false
					}
					if !isRetry {
						state = notify.LiveStateAutoRecordFailed
					}
					log.Warnf("开始录制房间 %d 失败（%d/%d）：%v", roomID, attempts, maxAutoStartAttempts, err)
				}
			}
		}

		if cfg != nil && cfg.Notify && (!isRetry || state == notify.LiveStateAutoRecordRetryStarted) {
			s.notifySvc.PublishLiveState(roomID, info.Uname, info.Title, state)
		}
		if markNow {
			s.autoStartRetry.Delete(roomID)
			s.markSessionState(roomID, currentSessionKey)
		}
	}
}

func isTransientAutoStartError(err error) bool {
	if err == nil ||
		errors.Is(err, recorder.ErrRecordingStarted) ||
		errors.Is(err, recorder.ErrRecordRecovering) ||
		errors.Is(err, recorder.ErrRecordingPending) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, recorder.ErrRoomBanned) ||
		errors.Is(err, recorder.ErrRoomEncrypted) {
		return false
	}
	return true
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
		log.Warnf("保存房间 %d 会话密钥失败：%v", roomID, err)
	}
}

func (s *Service) clearSessionState(roomID int) bool {
	s.autoStartRetry.Delete(roomID)
	_, loaded := s.sessionKeys.LoadAndDelete(roomID)
	if !loaded {
		return false
	}
	if err := s.bucket.Delete([]byte(strconv.Itoa(roomID))); err != nil {
		log.Warnf("清理房间 %d 会话状态失败：%v", roomID, err)
	}
	return true
}

func (s *Service) invalidateStaleRooms(rooms map[int]*subscribe.RoomConfig) {
	staleRooms := make([]int, 0)
	s.sessionKeys.Range(func(key int, value string) bool {
		if _, ok := rooms[key]; !ok {
			staleRooms = append(staleRooms, key)
		}
		return true
	})
	for _, roomID := range staleRooms {
		s.clearSessionState(roomID)
		s.m.UnregisterLiveRoom(roomID)
		log.Debugf("removed stale session state for room: %v", roomID)
	}
}

func (s *Service) getCachedRoomInfos(roomIDs []int) map[int]*bilibili.LiveRoomInfoDetail {
	out := make(map[int]*bilibili.LiveRoomInfoDetail, len(roomIDs))
	if len(roomIDs) == 0 {
		return out
	}
	infos, err := s.roomSvc.GetMultipleRoomInfos(roomIDs...)
	if err != nil {
		log.Warnf("读取房间信息缓存失败：%v", err)
		return out
	}
	for _, roomID := range roomIDs {
		if info, ok := infos[strconv.Itoa(roomID)]; ok && info != nil {
			out[roomID] = info
		}
	}
	return out
}

func (s *Service) getNotifyRoomInfos(liveCheckRoomIDs []int) map[int]*bilibili.LiveRoomInfoDetail {
	notifyRoomInfos := make(map[int]*bilibili.LiveRoomInfoDetail)

	if len(liveCheckRoomIDs) > 0 {
		infos, err := s.roomSvc.RefreshRoomInfos(liveCheckRoomIDs...)
		if err != nil {
			log.Warnf("强制刷新房间信息失败：%v，回退到逐房间检查", err)
			for _, roomID := range liveCheckRoomIDs {
				one, checkErr := s.roomSvc.RefreshRoomInfos(roomID)
				if checkErr != nil {
					log.Warnf("获取房间 %d 信息失败：%v", roomID, checkErr)
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
