package notify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/eric2788/bilirec/internal/modules/config"
	"github.com/eric2788/bilirec/pkg/db"
	"github.com/eric2788/bilirec/pkg/ds"
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
	state      ds.Atomic[*ServiceState]
}

type ServiceState struct {
	Enabled    bool
	Subscriber string
	PublicKey  string
	privateKey string
}

var logger = logrus.WithField("service", "notify")

const (
	webPushSubscriptionBucket = "WebPush_Subscriptions"
	webPushPublicKeyFileName  = "_webpush_public_key"
	webPushPrivateKeyFileName = "_webpush_private_key"
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
				logger.Info("web push is disabled: set WEBPUSH_SUBSCRIBER to enable")
				return nil // allow to be disabled
			}

			publicKey, privateKey, err := loadOrCreateVAPIDKeys(cfg.SecretDir)
			if err != nil {
				return fmt.Errorf("failed to initialize vapid keys: %w", err)
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

func (s *Service) Publish(event Event) {
	go s.PublishJSON(event)
}

func (s *Service) WebPushServiceState() *ServiceState {
	state, _ := s.state.Load()
	return &ServiceState{
		Enabled:    state.Enabled,
		Subscriber: state.Subscriber,
		PublicKey:  state.PublicKey,
	}
}

func (s *Service) AddWebPushSubscription(sub webpush.Subscription) error {
	if sub.Endpoint == "" || sub.Keys.Auth == "" || sub.Keys.P256dh == "" {
		return fmt.Errorf("invalid subscription")
	}

	payload, err := json.Marshal(sub)
	if err != nil {
		return fmt.Errorf("failed to marshal subscription: %w", err)
	}

	if err := s.bucket.Put([]byte(sub.Endpoint), payload); err != nil {
		return err
	}

	return nil
}

func (s *Service) RemoveWebPushSubscription(endpoint string) error {
	if endpoint == "" {
		return fmt.Errorf("invalid endpoint")
	} else if err := s.bucket.Delete([]byte(endpoint)); err != nil {
		return err
	}
	return nil
}

func (s *Service) PublishJSON(v any) {
	payload, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.publishPayload(payload)
}

func (s *Service) publishPayload(payload []byte) {
	state, _ := s.state.Load()
	if !state.Enabled {
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
			logger.Warnf("failed to send web push notification to %s: %v", sub.Endpoint, sendErr)
		}

		return nil
	})

	for _, endpoint := range staleEndpoints {
		if err := s.bucket.Delete([]byte(endpoint)); err != nil {
			logger.Warnf("failed to delete stale web push subscription: %s", endpoint)
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

func (s *Service) PublishLive(roomID int, streamer string, roomTitle string, autoRecordStarted bool) {
	message := "直播間已開播"
	eventType := "live_detected"
	if autoRecordStarted {
		message = "直播間已開播並已啟動自動錄製"
		eventType = "live_auto_record_started"
	}

	logger.Infof("pushing notification for room %d (%s): %s", roomID, streamer, message)

	s.Publish(Event{
		Type:      eventType,
		RoomID:    roomID,
		Streamer:  streamer,
		RoomTitle: roomTitle,
		Message:   message,
		Timestamp: time.Now().Unix(),
	})
}
