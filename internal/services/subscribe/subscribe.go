package subscribe

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/room"
	"github.com/bilirec/bilirec/pkg/db"
	"github.com/bilirec/bilirec/pkg/logger"
	"go.etcd.io/bbolt"
	"go.uber.org/fx"
)

const roomSubscribeBucket = "Room_Subscriptions"

var log = logger.Named("subscribe")

var (
	ErrRoomNotSubscribed     = errors.New("房间未订阅")
	ErrRoomAlreadySubscribed = errors.New("房间已订阅")
)

type Service struct {
	bucket  *db.Bucket
	roomSvc *room.Service

	// caches
	roomsMu sync.RWMutex
	rooms   map[int]*RoomConfig
}

func NewService(lc fx.Lifecycle, cfg *config.Config, roomSvc *room.Service) *Service {
	s := &Service{
		roomSvc: roomSvc,
		rooms:   make(map[int]*RoomConfig),
	}

	lc.Append(fx.StartStopHook(
		func() error {
			if client, err := db.Open(cfg.DatabaseDir + string(os.PathSeparator) + "subscribes.db"); err != nil {
				return err
			} else if bucket, err := client.Bucket(roomSubscribeBucket); err != nil {
				return err
			} else {
				s.bucket = bucket
			}
			return s.loadRooms()
		},
		func() error {
			if s.bucket == nil {
				return nil
			}
			return s.bucket.Close()
		},
	))
	return s
}

func (s *Service) Subscribe(roomID int) error {
	// room existence check
	if _, err := s.roomSvc.GetLiveRoomInfo(roomID); err != nil {
		return err
	}
	key := fmt.Append(nil, roomID)
	s.roomsMu.Lock()
	defer s.roomsMu.Unlock()
	if err := s.bucket.Update(func(bucket *bbolt.Bucket) error {
		exists := bucket.Get(key)
		if exists != nil {
			return ErrRoomAlreadySubscribed
		}
		return bucket.Put(key, defaultRoomConfigBytes)
	}); err != nil {
		return err
	}
	s.rooms[roomID] = defaultRoomConfig()
	return nil
}

func (s *Service) Unsubscribe(roomID int) error {
	key := fmt.Append(nil, roomID)
	s.roomsMu.Lock()
	defer s.roomsMu.Unlock()
	if err := s.bucket.Update(func(bucket *bbolt.Bucket) error {
		exists := bucket.Get(key)
		if exists == nil {
			return ErrRoomNotSubscribed
		}
		return bucket.Delete(key)
	}); err != nil {
		return err
	}
	delete(s.rooms, roomID)
	return nil
}

func (s *Service) IsSubscribed(roomID int) (bool, error) {
	s.roomsMu.RLock()
	defer s.roomsMu.RUnlock()
	_, ok := s.rooms[roomID]
	return ok, nil
}

func (s *Service) ListSubscribedRooms() ([]int, error) {
	s.roomsMu.RLock()
	defer s.roomsMu.RUnlock()
	roomIDs := make([]int, 0, len(s.rooms))
	for roomID := range s.rooms {
		roomIDs = append(roomIDs, roomID)
	}
	slices.Sort(roomIDs)
	return roomIDs, nil
}
