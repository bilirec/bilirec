package subscribe

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/room"
	"github.com/bilirec/bilirec/pkg/db"
	"github.com/sirupsen/logrus"
	"go.etcd.io/bbolt"
	"go.uber.org/fx"
)

const roomSubscribeBucket = "Room_Subscriptions"

var logger = logrus.WithField("service", "subscribe")

var (
	ErrRoomNotSubscribed     = errors.New("房间未订阅")
	ErrRoomAlreadySubscribed = errors.New("房间已订阅")
)

type Service struct {
	bucket  *db.Bucket
	roomSvc *room.Service
}

func NewService(lc fx.Lifecycle, cfg *config.Config, roomSvc *room.Service) *Service {
	s := &Service{
		roomSvc: roomSvc,
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
			return nil
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
	return s.bucket.Update(func(bucket *bbolt.Bucket) error {
		exists := bucket.Get(key)
		if exists != nil {
			return ErrRoomAlreadySubscribed
		}
		return bucket.Put(key, defaultRoomConfigBytes)
	})
}

func (s *Service) Unsubscribe(roomID int) error {
	key := fmt.Append(nil, roomID)
	return s.bucket.Update(func(bucket *bbolt.Bucket) error {
		exists := bucket.Get(key)
		if exists == nil {
			return ErrRoomNotSubscribed
		}
		return bucket.Delete(key)
	})
}

func (s *Service) IsSubscribed(roomID int) (bool, error) {
	key := fmt.Append(nil, roomID)
	return s.bucket.Exists(key)
}

func (s *Service) ListSubscribedRooms() ([]int, error) {
	var roomIDs []int
	err := s.bucket.ForEach(func(k, v []byte) error {
		roomID, err := strconv.Atoi(string(k))
		if err != nil {
			logger.Warnf("扫描条目失败：%s：%v，已忽略。", string(k), err)
			return nil
		}
		roomIDs = append(roomIDs, roomID)
		return nil
	})
	return roomIDs, err
}
