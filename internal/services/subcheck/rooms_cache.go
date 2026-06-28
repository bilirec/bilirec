package subcheck

import (
	"maps"
	"sync"
	"time"

	"github.com/bilirec/bilirec/internal/services/subscribe"
	"golang.org/x/sync/singleflight"
)

type subscribedRoomsCache struct {
	mu       sync.Mutex
	rooms    map[int]*subscribe.RoomConfig
	loadedAt time.Time
	interval time.Duration
	sf       singleflight.Group
}

func newSubscribedRoomsCache(interval time.Duration) subscribedRoomsCache {
	return subscribedRoomsCache{interval: interval}
}

func intervalStart(t time.Time, d time.Duration) time.Time {
	return t.Truncate(d)
}

func (c *subscribedRoomsCache) isStale(now time.Time) bool {
	if c.rooms == nil {
		return true
	}
	return intervalStart(c.loadedAt, c.interval).Before(intervalStart(now, c.interval))
}

func (c *subscribedRoomsCache) get(
	load func() (map[int]*subscribe.RoomConfig, error),
	now time.Time,
) (map[int]*subscribe.RoomConfig, error) {
	c.mu.Lock()
	if !c.isStale(now) {
		copy := maps.Clone(c.rooms)
		c.mu.Unlock()
		return copy, nil
	}
	c.mu.Unlock()

	v, err, _ := c.sf.Do("subscribed-rooms", func() (any, error) {
		c.mu.Lock()
		defer c.mu.Unlock()

		if !c.isStale(now) {
			return maps.Clone(c.rooms), nil
		}

		rooms, err := load()
		if err != nil {
			return nil, err
		}
		c.rooms = rooms
		c.loadedAt = now
		return maps.Clone(rooms), nil
	})
	if err != nil {
		return nil, err
	}
	return v.(map[int]*subscribe.RoomConfig), nil
}

func (c *subscribedRoomsCache) setInterval(d time.Duration) {
	c.mu.Lock()
	c.interval = d
	c.mu.Unlock()
}

func (s *Service) getSubscribedRooms() (map[int]*subscribe.RoomConfig, error) {
	return s.roomsCache.get(s.subSvc.ListSubscribedRoomsWithConfig, time.Now())
}
