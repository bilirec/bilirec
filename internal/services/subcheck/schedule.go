package subcheck

import (
	"math"
	"time"

	"github.com/bilirec/bilirec/internal/services/subscribe"
)

const (
	defaultRoomsPerShard   = 50
	defaultTickSecs        = 10
	defaultMinIntervalSecs = 60
	defaultMaxIntervalSecs = 300
	defaultMaxShards       = 32
	rescaleCooldown        = 30 * time.Second
)

type scheduleParams struct {
	roomsPerShard int
	tickSecs      int
	minInterval   time.Duration
	maxInterval   time.Duration
	maxShards     int
}

type schedule struct {
	shards   int
	interval time.Duration
}

func scheduleParamsFromConfig(roomsPerShard, tickSecs, minIntervalSecs, maxIntervalSecs, maxShards int) scheduleParams {
	p := scheduleParams{
		roomsPerShard: roomsPerShard,
		tickSecs:      tickSecs,
		minInterval:   time.Duration(minIntervalSecs) * time.Second,
		maxInterval:   time.Duration(maxIntervalSecs) * time.Second,
		maxShards:     maxShards,
	}
	if p.roomsPerShard <= 0 {
		p.roomsPerShard = defaultRoomsPerShard
	}
	if p.tickSecs <= 0 {
		p.tickSecs = defaultTickSecs
	}
	if p.minInterval <= 0 {
		p.minInterval = defaultMinIntervalSecs * time.Second
	}
	if p.maxInterval <= 0 {
		p.maxInterval = defaultMaxIntervalSecs * time.Second
	}
	if p.maxShards <= 0 {
		p.maxShards = defaultMaxShards
	}
	return p
}

func computeSchedule(roomCount int, p scheduleParams) schedule {
	if p.roomsPerShard <= 0 {
		p.roomsPerShard = defaultRoomsPerShard
	}
	if p.tickSecs <= 0 {
		p.tickSecs = defaultTickSecs
	}
	if p.minInterval <= 0 {
		p.minInterval = defaultMinIntervalSecs * time.Second
	}
	if p.maxInterval <= 0 {
		p.maxInterval = defaultMaxIntervalSecs * time.Second
	}
	if p.maxShards <= 0 {
		p.maxShards = defaultMaxShards
	}

	shards := 1
	if roomCount > 0 {
		shards = int(math.Ceil(float64(roomCount) / float64(p.roomsPerShard)))
	}
	if shards > p.maxShards {
		shards = p.maxShards
	}

	interval := time.Duration(shards*p.tickSecs) * time.Second
	if interval < p.minInterval {
		interval = p.minInterval
	}
	if interval > p.maxInterval {
		interval = p.maxInterval
	}

	return schedule{shards: shards, interval: interval}
}

func countLiveCheckRooms(rooms map[int]*subscribe.RoomConfig) int {
	n := 0
	for _, cfg := range rooms {
		if needsLiveAction(cfg) {
			n++
		}
	}
	return n
}

func (s *Service) countLiveCheckRooms() (int, error) {
	rooms, err := s.subSvc.ListSubscribedRoomsWithConfig()
	if err != nil {
		return 0, err
	}
	return countLiveCheckRooms(rooms), nil
}

func (s *Service) maybeRescale() {
	if err := s.ctx.Err(); err != nil {
		return
	}

	roomCount, err := s.countLiveCheckRooms()
	if err != nil {
		log.Warnf("统计订阅检查房间数失败：%v", err)
		return
	}

	desired := computeSchedule(roomCount, s.scheduleParams)

	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()

	if desired.shards == s.shardCount && desired.interval == s.checkInterval {
		return
	}
	if !s.lastRescale.IsZero() && time.Since(s.lastRescale) < rescaleCooldown {
		return
	}

	s.coordinator.SetCyclePeriod(desired.interval)
	s.shardCount = desired.shards
	s.checkInterval = desired.interval
	s.lastRescale = time.Now()

	log.Infof("subcheck 调度已调整：rooms=%d shards=%d interval=%s", roomCount, desired.shards, desired.interval)
}
