package danmaku

import (
	"context"
	"sync"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/puzpuzpuz/xsync/v4"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

var logger = logrus.WithField("service", "danmaku")

// Service records live danmaku (chat) into per-segment sidecar files paired
// with the recorder's video output. It is deliberately decoupled from the
// video pipeline: no shared locks, no blocking calls, message loss is acceptable.
//
// Constructing the service allocates nothing beyond an empty map; connection
// loops, goroutines and the buffer pool only exist while a session runs.
type Service struct {
	bilic    *bilibili.Client
	sessions *xsync.Map[int, *session]
	metrics  *metrics.Exporter

	poolOnce sync.Once
	pool     *pool.BytesPool

	outputFormat string
	wg           sync.WaitGroup
}

func NewService(
	lc fx.Lifecycle,
	cfg *config.Config,
	bilic *bilibili.Client,
	m *metrics.Exporter,
) *Service {
	s := &Service{
		outputFormat: cfg.DanmakuOutputFormat,
		bilic:        bilic,
		sessions:     xsync.NewMap[int, *session](),
		metrics:      m,
	}
	lc.Append(fx.StopHook(func() {
		s.sessions.Range(func(_ int, sess *session) bool {
			sess.cancel()
			return true
		})
		s.wg.Wait()
	}))
	return s
}

// StartSession begins danmaku recording for a room. videoPath is the current
// video segment path; the sidecar file is written next to it with the same
// base name. If a live session already exists for the room, the call is a no-op.
func (s *Service) StartSession(roomID int, recCtx context.Context, videoPath string, meta RoomMeta, segmentStart time.Time) {
	if existing, ok := s.sessions.Load(roomID); ok {
		if !existing.isDone() {
			return
		}
		s.removeSession(roomID, existing)
	}

	enc, err := NewFormatEncoder(s.outputFormat)
	if err != nil {
		logger.Errorf("房间 %d 弹幕输出格式无效：%v", roomID, err)
		return
	}

	s.poolOnce.Do(func() {
		s.pool = pool.NewBytesPool(config.ReadOnly.DanmakuBytesPoolSize())
	})

	sess := newSession(roomID, meta, s, enc, recCtx)
	s.sessions.Store(roomID, sess)
	s.metrics.DanmakuSessionStarted(roomID)

	s.wg.Go(func() {
		sess.supervise()
	})
	s.wg.Go(func() {
		sess.writeLoop(videoPath, segmentStart)
	})

	logger.Infof("房间 %d 弹幕录制已开始：%s", roomID, PathForVideo(videoPath, enc.Ext()))
}

// ActiveSessions reports how many rooms currently have a danmaku session.
// When danmaku recording is disabled this is always zero.
func (s *Service) ActiveSessions() int {
	return s.sessions.Size()
}

// GetBytesWritten returns the total encoded bytes accepted by the danmaku
// writer for the room's current session. It returns zero when no session exists.
func (s *Service) GetBytesWritten(roomID int) uint64 {
	sess, ok := s.sessions.Load(roomID)
	if !ok {
		return 0
	}
	return sess.bytesWritten.Load()
}

// removeSession deletes the mapping only if it still points at sess.
func (s *Service) removeSession(roomID int, sess *session) {
	removed := false
	s.sessions.Compute(roomID, func(old *session, loaded bool) (*session, xsync.ComputeOp) {
		if loaded && old == sess {
			removed = true
			return nil, xsync.DeleteOp
		}
		return old, xsync.CancelOp
	})
	if removed {
		s.metrics.DanmakuSessionStopped(roomID)
	}
}

// Rotate notifies the room's session that the video rotated to a new segment.
// It never blocks; if the previous rotation is still pending the signal is
// dropped and the session keeps writing to the old file.
func (s *Service) Rotate(roomID int, newVideoPath string, newSegmentStart time.Time) {
	sess, ok := s.sessions.Load(roomID)
	if !ok {
		return
	}
	select {
	case sess.rotateCh <- rotateRequest{videoPath: newVideoPath, segmentStart: newSegmentStart}:
		s.metrics.DanmakuRotation(roomID)
	default:
		logger.Warnf("房间 %d 弹幕分段轮换信号被丢弃（上一个轮换尚未处理）", roomID)
		s.metrics.DanmakuRotationDropped(roomID)
	}
}
