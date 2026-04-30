package notify

import (
	"context"
	"net/http"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/eric2788/bilirec/internal/modules/config"
	push "github.com/eric2788/bilirec/pkg/webpush"
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
	webPushManager *push.Manager
}

var logger = logrus.WithField("service", "notify")

func NewService(lc fx.Lifecycle, cfg *config.Config) *Service {
	webPushSubscriber := cfg.WebPushSubscriber
	secretDir := cfg.SecretDir

	s := &Service{
		webPushManager: push.NewManager(
			webPushSubscriber,
			secretDir,
			&http.Client{Timeout: 8 * time.Second},
		),
	}

	lc.Append(fx.StartHook(func(context.Context) error {
		if strings.TrimSpace(webPushSubscriber) == "" {
			logger.Info("web push is disabled: set WEBPUSH_SUBSCRIBER to enable")
			return nil
		}
		err := s.webPushManager.Start()
		if err != nil {
			logger.Errorf("web push is disabled: failed to initialize vapid keys: %v", err)
			return nil
		}
		return nil
	}))

	return s
}

func (s *Service) Publish(event Event) {
	go s.webPushManager.PublishJSON(event)
}

func (s *Service) WebPushEnabled() bool {
	return s.webPushManager.Enabled()
}

func (s *Service) WebPushPublicKey() string {
	return s.webPushManager.PublicKey()
}

func (s *Service) AddWebPushSubscription(sub webpush.Subscription) bool {
	return s.webPushManager.AddSubscription(sub)
}

func (s *Service) RemoveWebPushSubscription(endpoint string) bool {
	return s.webPushManager.RemoveSubscription(endpoint)
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
