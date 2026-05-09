package processors

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

type periodicFileSync struct {
	cancel context.CancelFunc
	wait   sync.WaitGroup
}

func startPeriodicFileSync(mu *sync.Mutex, file *os.File, logger *logrus.Entry, interval time.Duration) *periodicFileSync {
	ctx, cancel := context.WithCancel(context.Background())
	state := &periodicFileSync{cancel: cancel}
	state.wait.Add(1)

	go func() {
		defer state.wait.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				mu.Lock()
				if err := file.Sync(); err != nil {
					logger.Warnf("error syncing file: %v", err)
				}
				mu.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()

	return state
}

func (s *periodicFileSync) Stop() {
	s.cancel()
	s.wait.Wait()
}
