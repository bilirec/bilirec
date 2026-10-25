package subtitle

import (
	"sync"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/logger"
)

var log = logger.Named("subtitle")

// Service extracts bilibili live AI subtitles into per-segment .vtt sidecars.
// SEI parsing stays in this package (product-specific, not a shared library).
// Writer buffer settings match danmaku sidecars and are snapshotted at construction.
type Service struct {
	writer sidecarWriterOpts

	mu    sync.Mutex
	done  map[string]chan struct{}
	idle  sync.WaitGroup
}

type sidecarWriterOpts struct {
	bufferSize            int
	flushPeriod           time.Duration
	syncPeriod            time.Duration
	minPeriodicFlushBytes int
	sequentialWrite       bool
}

func NewService() *Service {
	return &Service{
		writer: sidecarWriterOpts{
			bufferSize:            config.ReadOnly.DanmakuWriterBufferSize(),
			flushPeriod:           time.Duration(config.ReadOnly.DanmakuWriterFlushPeriodSecs()) * time.Second,
			syncPeriod:            time.Duration(config.ReadOnly.DanmakuWriterSyncPeriodSecs()) * time.Second,
			minPeriodicFlushBytes: config.ReadOnly.DanmakuWriterMinPeriodicFlushBytes(),
			sequentialWrite:       config.ReadOnly.SequentialWrite(),
		},
		done: make(map[string]chan struct{}),
	}
}

func (s *Service) track(videoPath string, done chan struct{}) {
	s.mu.Lock()
	s.done[videoPath] = done
	s.mu.Unlock()
}

func (s *Service) untrack(videoPath string, done chan struct{}) {
	s.mu.Lock()
	if cur, ok := s.done[videoPath]; ok && cur == done {
		delete(s.done, videoPath)
	}
	s.mu.Unlock()
}

// Wait blocks until the segment consumer for videoPath has finished, or returns
// immediately if no subtitle scan is registered for that path.
func (s *Service) Wait(videoPath string) {
	s.mu.Lock()
	done, ok := s.done[videoPath]
	s.mu.Unlock()
	if !ok {
		return
	}
	<-done
}

// WaitIdle waits for all in-flight segment subtitle consumers (process shutdown).
func (s *Service) WaitIdle() {
	s.idle.Wait()
}
