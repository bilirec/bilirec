package danmaku

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/processors"
	"github.com/bilirec/bilirec/pkg/backoff"
	"github.com/bilirec/bilirec/pkg/pipeline"
)

const (
	danmakuEventType   = "danmaku"
	superChatEventType = "super_chat"
	giftEventType      = "gift"
	guardEventType     = "guard"
)

// rotateRequest asks the writer goroutine to finalize the current danmaku file
// and start a new one paired with the next video segment.
type rotateRequest struct {
	videoPath    string
	segmentStart time.Time
}

// session is the per-room danmaku recording worker: one connection supervisor
// goroutine and one writer goroutine. It never blocks the recorder. When the
// message channel is full, DANMAKU_OVERFLOW_POLICY decides whether to drop
// (default) or block the websocket handler until space is available.
type session struct {
	roomID  int
	meta    RoomMeta
	svc     *Service
	encoder FormatEncoder

	ctx    context.Context
	cancel context.CancelFunc

	msgCh          chan []byte
	rotateCh       chan rotateRequest
	supervisorDone chan struct{}
	done           chan struct{}

	segmentStartNano atomic.Int64
	dropped          atomic.Uint64
	bytesWritten     atomic.Uint64

	// writer state owned by writeLoop; nil between segments.
	writer    *pipeline.Pipe[[]byte]
	curPath   string
	writerCtx context.Context
}

func newSession(roomID int, meta RoomMeta, svc *Service, enc FormatEncoder, recCtx context.Context) *session {
	ctx, cancel := context.WithCancel(recCtx)
	s := &session{
		roomID:         roomID,
		meta:           meta,
		svc:            svc,
		encoder:        enc,
		ctx:            ctx,
		cancel:         cancel,
		msgCh:          make(chan []byte, config.ReadOnly.DanmakuChanBufferSize()),
		rotateCh:       make(chan rotateRequest, 1),
		supervisorDone: make(chan struct{}),
		done:           make(chan struct{}),
	}
	return s
}

// isDone reports whether the connection supervisor has exited, i.e. the
// session is tearing down and a new one may replace it.
func (s *session) isDone() bool {
	select {
	case <-s.supervisorDone:
		return true
	default:
		return false
	}
}

func (s *session) segmentStart() time.Time {
	return time.Unix(0, s.segmentStartNano.Load())
}

// supervise runs the reconnect loop until the recording context is canceled.
// Danmaku loss is acceptable; the loop never gives up on its own.
func (s *session) supervise() {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("房间 %d 弹幕连接监督 panic：%v", s.roomID, r)
		}
	}()
	defer close(s.supervisorDone)
	defer close(s.msgCh)

	bo := backoff.NewSequence(2*time.Second, 2*time.Second, 2*time.Second, 5*time.Second, 10*time.Second, 15*time.Second)
	attempt := 0
	for {
		if s.ctx.Err() != nil {
			return
		}
		s.svc.metrics.DanmakuConnectionAttempt(s.roomID)
		if attempt > 0 {
			s.svc.metrics.DanmakuReconnect(s.roomID)
		}
		attempt++
		connectedAt := time.Now()
		err := s.runOnce()
		s.svc.metrics.DanmakuConnectionActive(s.roomID, false)
		if s.ctx.Err() != nil {
			return
		}
		if time.Since(connectedAt) > time.Minute {
			bo.Reset()
		}
		delay := bo.Next()
		log.Warnf("房间 %d 弹幕连接断开（%v），%v 后重连", s.roomID, err, delay)
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-s.ctx.Done():
			timer.Stop()
			return
		}
	}
}

func (s *session) runOnce() error {
	protocolRoomID := int(s.meta.RoomID)
	info, err := s.svc.bilic.GetDanmuInfo(s.ctx, protocolRoomID)
	if err != nil {
		return err
	}
	uid, buvid := s.svc.bilic.DanmakuIdentity()

	client := bilibili.NewLiveMessageClient(protocolRoomID, uid, buvid)
	client.HandleFunc("DANMU_MSG", s.handleDanmaku)
	client.HandleFunc("SUPER_CHAT_MESSAGE", s.handleSuperChat)
	client.HandleFunc("SEND_GIFT", s.handleGift)
	client.HandleFunc("GUARD_BUY", s.handleGuard)

	s.svc.metrics.DanmakuConnectionActive(s.roomID, true)
	// Like BililiveRecorder, use the first candidate host; the reconnect loop
	// refetches a fresh host list on every cycle.
	return client.Run(s.ctx, info.HostList[0], info.Token)
}

func (s *session) handleDanmaku(raw []byte) {
	e, ok := parseDanmaku(raw)
	if !ok {
		s.svc.metrics.DanmakuParseError(s.roomID)
		return
	}
	s.svc.metrics.DanmakuMessageReceived(s.roomID, danmakuEventType)
	s.enqueue(func(buf []byte, ts string) []byte {
		return s.encoder.AppendDanmaku(buf, e, ts)
	}, danmakuEventType)
}

func (s *session) handleSuperChat(raw []byte) {
	e := parseSuperChat(raw)
	s.svc.metrics.DanmakuMessageReceived(s.roomID, superChatEventType)
	s.enqueue(func(buf []byte, ts string) []byte {
		return s.encoder.AppendSuperChat(buf, e, ts)
	}, superChatEventType)
}

func (s *session) handleGift(raw []byte) {
	e := parseGift(raw)
	s.svc.metrics.DanmakuMessageReceived(s.roomID, giftEventType)
	s.enqueue(func(buf []byte, ts string) []byte {
		return s.encoder.AppendGift(buf, e, ts)
	}, giftEventType)
}

func (s *session) handleGuard(raw []byte) {
	e := parseGuard(raw)
	s.svc.metrics.DanmakuMessageReceived(s.roomID, guardEventType)
	s.enqueue(func(buf []byte, ts string) []byte {
		return s.encoder.AppendGuard(buf, e, ts)
	}, guardEventType)
}

type fragmentBuilder func(buf []byte, ts string) []byte

// enqueue builds a fragment into a pooled buffer and hands it to the writer
// goroutine. With the default "drop" policy a full channel discards the
// fragment; with "block" it waits until space is available or the session ends.
func (s *session) enqueue(build fragmentBuilder, eventType string) {
	buf := s.svc.pool.GetBytes()
	frag := build(buf[:0], formatRelativeTS(s.segmentStart(), time.Now()))
	if len(frag) == 0 {
		s.svc.pool.PutBytes(buf)
		return
	}
	if config.ReadOnly.DanmakuOverflowPolicy() == "block" {
		select {
		case s.msgCh <- frag:
		case <-s.ctx.Done():
			s.svc.pool.PutBytes(frag)
		}
	} else if config.ReadOnly.DanmakuOverflowPolicy() == "drop" {
		select {
		case s.msgCh <- frag:
		default:
			s.svc.pool.PutBytes(frag)
			s.svc.metrics.DanmakuMessageDropped(s.roomID, eventType)
			if dropped := s.dropped.Add(1); dropped == 1 || dropped%1000 == 0 {
				log.Warnf("房间 %d 弹幕写入通道已满，已累计丢弃 %d 条消息", s.roomID, dropped)
			}
		}
	} else {
		// should never reach here
		log.Errorf("房间 %d 未知弹幕溢出策略：%s", s.roomID, config.ReadOnly.DanmakuOverflowPolicy())
		s.svc.pool.PutBytes(frag)
	}
}

// writeLoop owns the danmaku writer pipeline: it consumes fragments, handles
// segment rotation and finalizes the file on shutdown.
func (s *session) writeLoop(videoPath string, segmentStart time.Time) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("房间 %d 弹幕写入 panic：%v", s.roomID, r)
		}
	}()
	defer close(s.done)
	defer s.svc.removeSession(s.roomID, s)

	// The recording context is canceled before the writer gets a chance to
	// consume the closed message channel. Keep a non-cancelable context for
	// the final footer and for draining the buffered writer.
	s.writerCtx = context.Background()
	s.openSegment(rotateRequest{videoPath: videoPath, segmentStart: segmentStart})

	for {
		select {
		case frag, ok := <-s.msgCh:
			if !ok {
				s.finalizeSegment()
				return
			}
			s.writeFragment(frag)
		case req := <-s.rotateCh:
			if s.drainPending() {
				s.finalizeSegment()
				return
			}
			s.rotateSegment(req)
		}
	}
}

func (s *session) openSegment(req rotateRequest) {
	p := PathForVideo(req.videoPath, s.encoder.Ext())
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		log.Errorf("房间 %d 创建弹幕目录失败：%v", s.roomID, err)
		return
	}

	writerPipeline := pipeline.New(
		processors.NewBufferedStreamWriter(
			p,
			processors.WithBufferSize(config.ReadOnly.DanmakuWriterBufferSize()),
			processors.WithFlushPeriod(time.Duration(config.ReadOnly.DanmakuWriterFlushPeriodSecs())*time.Second),
			processors.WithSyncPeriod(time.Duration(config.ReadOnly.DanmakuWriterSyncPeriodSecs())*time.Second),
			processors.WithChanBufferSize(config.ReadOnly.DanmakuChanBufferSize()),
			processors.WithBytesPool(s.svc.pool),
			processors.WithMinPeriodicFlushBytes(config.ReadOnly.DanmakuWriterMinPeriodicFlushBytes()),
			processors.WithSequentialWrite(config.ReadOnly.SequentialWrite()),
		),
	)
	if err := writerPipeline.Open(s.writerCtx); err != nil {
		log.Errorf("房间 %d 创建弹幕文件失败：%v", s.roomID, err)
		return
	}

	s.writer = writerPipeline
	s.curPath = p
	s.segmentStartNano.Store(req.segmentStart.UnixNano())

	header := s.svc.pool.GetBytes()
	h := s.encoder.AppendHeader(header[:0], s.meta, req.segmentStart)
	if len(h) > 0 {
		if _, err := s.writer.Process(s.writerCtx, h); err != nil {
			log.Errorf("房间 %d 写入弹幕文件头失败：%v", s.roomID, err)
		} else {
			s.bytesWritten.Add(uint64(len(h)))
			s.svc.metrics.AddDanmakuBytes(s.roomID, len(h))
		}
	}
	s.svc.pool.PutBytes(h)
}

func (s *session) finalizeSegment() {
	if s.writer == nil {
		return
	}
	footer := s.svc.pool.GetBytes()
	f := s.encoder.AppendFooter(footer[:0])
	if len(f) > 0 {
		if _, err := s.writer.Process(s.writerCtx, f); err != nil {
			log.Errorf("房间 %d 写入弹幕文件尾失败：%v", s.roomID, err)
		} else {
			s.bytesWritten.Add(uint64(len(f)))
			s.svc.metrics.AddDanmakuBytes(s.roomID, len(f))
		}
	}
	s.svc.pool.PutBytes(f)

	// BufferedStreamWriterProcessor.Close drains its input queue, flushes
	// the buffered writer, syncs the file and closes the file descriptor.
	s.writer.Close()
	s.writer = nil
	log.Infof("房间 %d 弹幕文件已写入：%s", s.roomID, s.curPath)
}

func (s *session) writeFragment(frag []byte) {
	if s.writer != nil {
		if _, err := s.writer.Process(s.writerCtx, frag); err != nil {
			log.Errorf("房间 %d 写入弹幕失败：%v", s.roomID, err)
		} else {
			s.bytesWritten.Add(uint64(len(frag)))
			s.svc.metrics.AddDanmakuBytes(s.roomID, len(frag))
		}
	}
	s.svc.pool.PutBytes(frag)
}

// drainPending writes all currently queued fragments so messages that
// arrived before a rotation signal stay in the old segment's file.
// Returns true when the channel was closed (session ending).
func (s *session) drainPending() bool {
	for {
		select {
		case frag, ok := <-s.msgCh:
			if !ok {
				return true
			}
			s.writeFragment(frag)
		default:
			return false
		}
	}
}

func (s *session) rotateSegment(req rotateRequest) {
	s.finalizeSegment()
	s.openSegment(req)
}
