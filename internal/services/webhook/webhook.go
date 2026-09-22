package webhook

import (
	"context"
	"sync"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/utils"
	"github.com/go-resty/resty/v2"
	"github.com/puzpuzpuz/xsync/v4"
	"go.uber.org/fx"
)

var log = logger.Named("webhook")

const maxAttempts = 3

// Service emits BililiveRecorder-compatible Webhook v2 JSON to configured URLs.
type Service struct {
	urls      []string
	outputDir string
	client    *resty.Client

	rooms    *xsync.Map[int, *roomQueue]
	pending  chan roomWork
	ctx      context.Context
	cancel   context.CancelFunc
	workerWg sync.WaitGroup

	convertMetaMu sync.Mutex
	convertMeta   map[string]*SegmentMeta
}

func NewService(lc fx.Lifecycle, cfg *config.Config) *Service {
	urls := config.ParseWebhookURLs(cfg.WebhookURLs)
	if len(urls) == 0 {
		return &Service{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	client := resty.New().
		SetTimeout(15*time.Second).
		SetHeader("Content-Type", "application/json").
		SetHeader("User-Agent", "bilirec/webhook")
	s := &Service{
		urls:        urls,
		outputDir:   cfg.OutputDir,
		client:      client,
		rooms:       xsync.NewMap[int, *roomQueue](),
		pending:     make(chan roomWork, pendingWorkCap),
		ctx:         ctx,
		cancel:      cancel,
		convertMeta: make(map[string]*SegmentMeta),
	}

	lc.Append(fx.StartStopHook(
		func() error {
			s.workerWg.Go(s.runWorker)
			return nil
		},
		func() error {
			s.cancel()
			s.workerWg.Wait()
			return nil
		},
	))
	return s
}

func (s *Service) enabled() bool {
	return s != nil && len(s.urls) > 0 && s.client != nil
}

func (s *Service) emit(eventType EventType, data roomEventData) {
	if !s.enabled() {
		return
	}
	eventID, err := utils.NewUUIDv4()
	if err != nil {
		log.Warnf("webhook 生成 EventId 失败：%v", err)
		return
	}
	body := envelope{
		EventType:      eventType,
		EventTimestamp: time.Now().Format(time.RFC3339Nano),
		EventID:        eventID,
		EventData:      data,
	}
	s.enqueue(data.RoomID, body)
}

func (s *Service) deliver(ctx context.Context, body envelope) {
	for _, url := range s.urls {
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			if ctx.Err() != nil {
				return
			}
			resp, err := s.client.R().
				SetContext(ctx).
				SetBody(body).
				Post(url)
			if err == nil && resp != nil && resp.StatusCode() >= 200 && resp.StatusCode() < 300 {
				break
			}
			if err != nil {
				log.Warnf("webhook POST %s 失败（%d/%d）：%v", url, attempt, maxAttempts, err)
			} else if resp != nil {
				log.Warnf("webhook POST %s 非 2xx（%d/%d）：%d", url, attempt, maxAttempts, resp.StatusCode())
			}
			if attempt == maxAttempts {
				break
			}
			if !sleepBackoff(ctx, time.Duration(attempt)*time.Second) {
				return
			}
		}
	}
}

func sleepBackoff(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
