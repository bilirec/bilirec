package notify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/eric2788/bilirec/internal/modules/config"
	"github.com/eric2788/bilirec/pkg/db"
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
}

type ServiceState struct {
	Enabled    bool
	Subscriber string
	PublicKey  string
	privateKey string
}

type LiveState string

var logger = logrus.WithField("service", "notify")

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

func (s *Service) WebPushServiceState() *ServiceState {
	state := s.state.Load()
	if state == nil {
		return &ServiceState{}
	}
	return &ServiceState{
		Enabled:    state.Enabled,
		Subscriber: state.Subscriber,
		PublicKey:  state.PublicKey,
	}
}

func (s *Service) AddWebPushSubscription(sub webpush.Subscription) error {
	if sub.Endpoint == "" || sub.Keys.Auth == "" || sub.Keys.P256dh == "" {
		return fmt.Errorf("无效的订阅")
	}

	payload, err := json.Marshal(sub)
	if err != nil {
		return fmt.Errorf("序列化订阅失败：%w", err)
	}

	if err := s.bucket.Put([]byte(sub.Endpoint), payload); err != nil {
		return err
	}

	return nil
}

func (s *Service) RemoveWebPushSubscription(endpoint string) error {
	if endpoint == "" {
		return fmt.Errorf("无效的订阅 endpoint")
	} else if err := s.bucket.Delete([]byte(endpoint)); err != nil {
		return err
	}
	return nil
}

func (s *Service) publishJSON(v any) {
	payload, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.publishPayload(payload)
}

func (s *Service) publishPayload(payload []byte) {
	state := s.state.Load()
	if state == nil || !state.Enabled {
		return
	}

	staleEndpoints := make([]string, 0, 8)

	_ = s.bucket.ForEach(func(k, v []byte) error {
		var sub webpush.Subscription
		if err := json.Unmarshal(v, &sub); err != nil {
			staleEndpoints = append(staleEndpoints, string(k))
			return nil
		}

		if sub.Endpoint == "" || sub.Keys.Auth == "" || sub.Keys.P256dh == "" {
			staleEndpoints = append(staleEndpoints, string(k))
			return nil
		}

		options := &webpush.Options{
			HTTPClient:      s.httpClient,
			Subscriber:      state.Subscriber,
			VAPIDPublicKey:  state.PublicKey,
			VAPIDPrivateKey: state.privateKey,
			TTL:             7200, // 2 hours
		}

		resp, sendErr := webpush.SendNotification(payload, &sub, options)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}

		if resp != nil && (resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound) {
			staleEndpoints = append(staleEndpoints, sub.Endpoint)
		}

		if sendErr != nil {
			logger.Warnf("向 %s 发送 Web Push 通知失败：%v", sub.Endpoint, sendErr)
		}

		return nil
	})

	for _, endpoint := range staleEndpoints {
		if err := s.bucket.Delete([]byte(endpoint)); err != nil {
			logger.Warnf("删除过期 Web Push 订阅失败：%s", endpoint)
		}
	}
}

func loadOrCreateVAPIDKeys(secretDir string) (string, string, error) {

	publicPath := filepath.Join(secretDir, webPushPublicKeyFileName)
	privatePath := filepath.Join(secretDir, webPushPrivateKeyFileName)

	publicBytes, publicErr := os.ReadFile(publicPath)
	privateBytes, privateErr := os.ReadFile(privatePath)

	publicKey := strings.TrimSpace(string(publicBytes))
	privateKey := strings.TrimSpace(string(privateBytes))
	if publicErr == nil && privateErr == nil && publicKey != "" && privateKey != "" {
		return publicKey, privateKey, nil
	}

	generatedPrivateKey, generatedPublicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return "", "", err
	}

	if err := os.WriteFile(publicPath, []byte(generatedPublicKey), 0600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(privatePath, []byte(generatedPrivateKey), 0600); err != nil {
		return "", "", err
	}

	return generatedPublicKey, generatedPrivateKey, nil
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

	go s.publishJSON(Event{
		Type:      string(state),
		RoomID:    roomID,
		Streamer:  streamer,
		RoomTitle: roomTitle,
		Message:   message,
		Timestamp: time.Now().Unix(),
	})
}
