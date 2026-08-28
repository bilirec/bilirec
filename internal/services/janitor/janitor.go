package janitor

import (
	"context"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"

	"github.com/bilirec/bilirec/internal/services/convert"
	"github.com/bilirec/bilirec/internal/services/recorder"
	"go.uber.org/fx"
)

var log = logger.Named("janitor")

const (
	janitorCheckInterval = 30 * time.Second
	janitorIdleGrace     = 2 * time.Minute
)

type Service struct {
	recorder   *recorder.Service
	convertSvc *convert.Service

	wg sync.WaitGroup
}

func NewService(lc fx.Lifecycle, recSvc *recorder.Service, convertSvc *convert.Service) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{recorder: recSvc, convertSvc: convertSvc}

	lc.Append(fx.StartStopHook(
		func() error {
			s.wg.Add(1)
			go s.run(ctx)
			return nil
		},
		func() error {
			cancel()
			s.wg.Wait()
			return nil
		},
	))
	return s
}

func (s *Service) run(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(janitorCheckInterval)
	defer ticker.Stop()

	var idleStart time.Time
	hasCleaned := true // 💡 初始状态假设已经清理过了，直到第一次检测到空闲时才执行清理

	for {
		select {
		case <-ticker.C:
			recorderCount := s.recorder.ListRecordingSize()
			convertCount := s.convertSvc.InProgressSize()

			// 狀態：系統正在運作中
			if recorderCount > 0 || convertCount > 0 {
				if !idleStart.IsZero() {
					log.Debugf("退出空闲窗口：recorders=%d, converts=%d", recorderCount, convertCount)
					idleStart = time.Time{} // 重置空閒時間
				}
				hasCleaned = false // 只要有任务在进行，就标记为未清理过，等待下一个空闲窗口
				continue
			}

			if hasCleaned {
				continue // 已经清理过了，继续等待下一个空闲窗口
			}

			if idleStart.IsZero() {
				log.Debugf("进入空闲窗口：recorders=%d, converts=%d, 开始倒计时", recorderCount, convertCount)
				idleStart = time.Now()
				continue
			}

			if time.Since(idleStart) >= janitorIdleGrace {
				s.performCleanup()
				hasCleaned = true // 💡 標記為已清理，直到有新任務進來前，都不會再觸發 GC
			}

		case <-ctx.Done():
			return
		}
	}
}

func (s *Service) performCleanup() {
	log.Info("当前无进行中的录制和转码，执行维护性 GC")

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	log.Infof("清理前: Alloc=%d MB, Sys=%d MB, NumGC=%d",
		m.Alloc/1024/1024, m.Sys/1024/1024, m.NumGC)

	runtime.GC()
	runtime.GC() // 再执行一次GC，确保所有对象都被回收
	debug.FreeOSMemory()

	runtime.ReadMemStats(&m)
	log.Infof("清理后：Alloc=%d MB，Sys=%d MB",
		m.Alloc/1024/1024, m.Sys/1024/1024)
}
