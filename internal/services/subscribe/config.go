package subscribe

import (
	"fmt"

	"github.com/bilirec/bilirec/pkg/pool"
	"go.etcd.io/bbolt"
)

type RoomConfig struct {
	AutoRecord            bool
	Notify                bool
	RecordDurationMinutes int      // 0 = system default, -1 = unlimited, >0 = custom minutes
	Qn                    int      // 0 = backend default (原画); see bilibili.Quality
	OnlyAudio             bool     // request audio-only stream when starting recording
	RecordDanmaku         bool     // record live chat sidecar alongside video when starting recording
	StreamProfiles        []string // empty/nil = auto (all formats); allow-list of http-flv|hls-ts|hls-fmp4
}

var roomConfigSerializer = pool.NewSerializer()
var defaultRoomConfigBytes = mustSerializeRoomConfig(defaultRoomConfig())

func mustSerializeRoomConfig(cfg *RoomConfig) []byte {
	data, err := roomConfigSerializer.Serialize(cfg)
	if err != nil {
		panic(err)
	}
	return data
}

func defaultRoomConfig() *RoomConfig {
	return &RoomConfig{AutoRecord: false, Notify: false, RecordDurationMinutes: 0}
}

func (s *Service) UpdateConfig(roomID int, cfg *RoomConfig) error {
	var data []byte
	if cfg == nil {
		data = defaultRoomConfigBytes
	} else {
		encoded, err := roomConfigSerializer.Serialize(cfg)
		if err != nil {
			return err
		}
		data = encoded
	}

	key := fmt.Append(nil, roomID)
	s.roomsMu.Lock()
	defer s.roomsMu.Unlock()
	if err := s.bucket.Update(func(bucket *bbolt.Bucket) error {
		exists := bucket.Get(key)
		if exists == nil {
			return ErrRoomNotSubscribed
		}
		return bucket.Put(key, data)
	}); err != nil {
		return err
	}
	s.rooms[roomID] = cloneRoomConfig(cfg)
	return nil
}

func (s *Service) ListSubscribedRoomsWithConfig() (map[int]*RoomConfig, error) {
	s.roomsMu.RLock()
	defer s.roomsMu.RUnlock()
	return cloneRooms(s.rooms), nil
}

func parseRoomConfig(raw []byte) (*RoomConfig, error) {
	if len(raw) == 0 {
		return defaultRoomConfig(), nil
	}

	cfg := &RoomConfig{}
	if err := roomConfigSerializer.Deserialize(raw, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (s *Service) GetConfig(roomID int) (*RoomConfig, error) {
	s.roomsMu.RLock()
	defer s.roomsMu.RUnlock()
	cfg, ok := s.rooms[roomID]
	if !ok {
		return nil, ErrRoomNotSubscribed
	}
	return cloneRoomConfig(cfg), nil
}
