package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/db"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

type Event struct {
	Type      string `json:"type"`
	RoomID    int    `json:"room_id"`
	Streamer  string `json:"streamer_name"`
	RoomTitle string `json:"room_title"`
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
}

type Service struct {
	httpClient *http.Client
	bucket     *db.Bucket
	state      atomic.Pointer[ServiceState]

	sseToken   string
	sseClients map[uint64]chan []byte
	sseMu      sync.RWMutex
	sseSeq     atomic.Uint64
}

type ServiceState struct {
	Enabled    bool
	Subscriber string
	PublicKey  string
	privateKey string
}

type LiveState string

var logger = logrus.WithField("service", "notify")

var (
	ErrSSEDisabled     = errors.New("sse is disabled")
	ErrSSETokenMissing = errors.New("sse token is required")
	ErrSSETokenInvalid = errors.New("sse token is invalid")
)

const (
	webPushSubscriptionBucket = "WebPush_Subscriptions"
	webPushPublicKeyFileName  = "_webpush_public_key"
	webPushPrivateKeyFileName = "_webpush_private_key"
)

const (
	LiveStateLiveDetected      LiveState = "live_detected"
	LiveStateAutoRecordStarted LiveState = "live_auto_record_started"
	LiveStateAutoRecordFailed  LiveState = "live_auto_record_failed"
	LiveStateLiveEnded         LiveState = "live_ended"
	LiveStateRecordStopped     LiveState = "live_record_stopped"
)

func NewService(lc fx.Lifecycle, cfg *config.Config) (*Service, error) {

	s := &Service{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		sseToken:   cfg.NotifySSEToken,
		sseClients: make(map[uint64]chan []byte),
	}

	lc.Append(fx.StartStopHook(
		func() error {
			client, err := db.Open(cfg.DatabaseDir + string(os.PathSeparator) + "notify.db")
			if err != nil {
				return err
			}

			bucket, err := client.Bucket(webPushSubscriptionBucket)
			if err != nil {
				return err
			}
			s.bucket = bucket

			if strings.TrimSpace(cfg.WebPushSubscriber) == "" {
				s.state.Store(&ServiceState{Enabled: false})
				logger.Info("Web Push 已禁用：设置 WEBPUSH_SUBSCRIBER 可启用")
				return nil // allow to be disabled
			}

			publicKey, privateKey, err := loadOrCreateVAPIDKeys(cfg.SecretDir)
			if err != nil {
				return fmt.Errorf("初始化 vapid 密钥失败：%w", err)
			}

			s.state.Store(&ServiceState{
				Enabled:    true,
				Subscriber: cfg.WebPushSubscriber,
				PublicKey:  publicKey,
				privateKey: privateKey,
			})
			return nil
		},
		func() error {
			return s.bucket.Close()
		},
	))

	return s, nil
}

func (s *Service) publishJSON(v any) {
	payload, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.publishPayload(payload)
}

func (s *Service) publishPayload(payload []byte) {
	go s.publishWebPushPayload(payload)
	go s.publishSSEPayload(payload)
}

func (s *Service) PublishLiveState(roomID int, streamer string, roomTitle string, state LiveState) {
	var message string
	switch state {
	case LiveStateLiveDetected:
		message = "直播間已開播"
	case LiveStateAutoRecordStarted:
		message = "直播間已開播並已啟動錄製"
	case LiveStateAutoRecordFailed:
		message = "直播間已開播但錄製啟動失敗"
	case LiveStateLiveEnded:
		message = "直播間已結束"
	case LiveStateRecordStopped:
		message = "直播間錄製已停止"
	}

	logger.Infof("正在推送房间 %d（%s）通知：%s", roomID, streamer, message)

	s.publishJSON(Event{
		Type:      string(state),
		RoomID:    roomID,
		Streamer:  streamer,
		RoomTitle: roomTitle,
		Message:   message,
		Timestamp: time.Now().Unix(),
	})
}
