package danmaku

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/internal/services/preview"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/bilirec/bilirec/utils"
	"github.com/puzpuzpuz/xsync/v4"
	"go.uber.org/fx"
)

var log = logger.Named("danmaku")

// Service records live danmaku (chat) into per-segment sidecar files paired
// with the recorder's video output. Danmaku rotates when the video segment
// starts; if the video file is never kept, Discard removes the sidecar. It is
// deliberately decoupled from the video pipeline: no shared locks, no blocking
// calls, message loss is acceptable.
//
// Constructing the service allocates nothing beyond an empty map; connection
// loops, goroutines and the buffer pool only exist while a session runs.
type Service struct {
	bilic    *bilibili.Client
	preview  *preview.Service
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
	previewSvc *preview.Service,
) *Service {
	s := &Service{
		outputFormat: cfg.DanmakuOutputFormat,
		bilic:        bilic,
		preview:      previewSvc,
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
		log.Errorf("房间 %d 弹幕输出格式无效：%v", roomID, err)
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

	log.Infof("房间 %d 弹幕录制已开始：%s", roomID, utils.SidecarPathForVideo(videoPath, enc.Ext()))
}

// IsSessionActive reports whether a room has a live danmaku recording session.
func (s *Service) IsSessionActive(roomID int) bool {
	sess, ok := s.sessions.Load(roomID)
	return ok && sess != nil && !sess.isDone()
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

// GetDropped returns messages discarded because the write channel was full.
// It returns zero when no session exists.
func (s *Service) GetDropped(roomID int) uint64 {
	sess, ok := s.sessions.Load(roomID)
	if !ok {
		return 0
	}
	return sess.dropped.Load()
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
		s.metrics.UnregisterDanmakuRoom(roomID)
	}
}

// Rotate notifies the room's session that the video rotated to a new segment.
// It never blocks. If the previous rotation is still pending it is superseded
// (latest wins), so the session never lingers on a segment the video already
// left.
func (s *Service) Rotate(roomID int, newVideoPath string, newSegmentStart time.Time) {
	sess, ok := s.sessions.Load(roomID)
	if !ok {
		return
	}
	req := rotateRequest{videoPath: newVideoPath, segmentStart: newSegmentStart}
	for range 2 {
		select {
		case sess.rotateCh <- req:
			s.metrics.DanmakuRotation(roomID)
			return
		default:
		}
		select {
		case <-sess.rotateCh:
			log.Warnf("房间 %d 上一个弹幕分段轮换尚未处理，已被新的轮换取代", roomID)
			s.metrics.DanmakuRotationDropped(roomID)
		default:
		}
	}
	log.Warnf("房间 %d 弹幕分段轮换信号被丢弃", roomID)
	s.metrics.DanmakuRotationDropped(roomID)
}

// Discard removes the sidecar for a video segment that was not kept on disk.
// It never blocks the recorder.
func (s *Service) Discard(roomID int, videoPath string) {
	if videoPath == "" {
		return
	}
	sess, ok := s.sessions.Load(roomID)
	if !ok {
		s.removeSidecarFile(videoPath)
		return
	}
	// The session goroutine owns the sidecar files, so it decides whether this
	// one is open, already closed or not opened yet.
	sess.queueDiscard(videoPath)
	if sess.exiting.Load() {
		// Its final applyDiscards may already be done.
		s.removeSidecarFile(videoPath)
	}
}

func (s *Service) removeSidecarFile(videoPath string) {
	enc, err := NewFormatEncoder(s.outputFormat)
	if err != nil {
		log.Errorf("弹幕输出格式无效，无法删除 sidecar：%v", err)
		return
	}
	p := utils.SidecarPathForVideo(videoPath, enc.Ext())
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		log.Warnf("删除弹幕文件失败：%s err=%v", p, err)
	}
}
