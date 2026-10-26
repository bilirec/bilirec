package preview

import (
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/ds"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/pkg/pipeline"
)

var log = logger.Named("preview")

const eventChanSize = 256

var (
	ErrNotRecording = errors.New("该房间当前没有可预览的录制")
)

type Service struct {
	mu                 sync.Mutex
	rooms              map[int]*roomState
	mediaMaxQueuedBytes int
}

type EventSub struct {
	C      <-chan []byte
	Leave  func()
	origin float64
}

type Media struct {
	MIME string
	Body io.ReadCloser
}

type roomState struct {
	path         string
	ext          string
	segmentStart time.Time

	bootstrapReady atomic.Bool
	bootstrap      []byte

	mediaN atomic.Int32
	media  map[uint64]*mediaSub

	eventSubs map[uint64]chan []byte
	nextSub   uint64
}

type mediaSub struct {
	id     uint64
	q      *ds.ChunkQueue[[]byte]
	closed atomic.Bool
	ext    string
	join   joinFilter
}

func NewService(cfg *config.Config) *Service {
	return &Service{
		rooms:               make(map[int]*roomState),
		mediaMaxQueuedBytes: config.PreviewMediaMaxQueuedBytesFrom(cfg),
	}
}

func (s *Service) Tap(roomID int, path, ext string, audioOnly bool) *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"preview-fanout",
		&fanoutProcessor{svc: s, roomID: roomID, path: path, ext: ext, audioOnly: audioOnly},
	)
}

func (s *Service) PublishEvent(roomID int, line []byte) {
	if len(line) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[roomID]
	if r == nil || len(r.eventSubs) == 0 {
		return
	}
	cp := append([]byte(nil), line...)
	s.broadcastLocked(r, cp)
}

func (s *Service) Subscribe(roomID int) (EventSub, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[roomID]
	if r == nil || r.path == "" {
		return EventSub{}, ErrNotRecording
	}
	id := r.nextSub + 1
	r.nextSub = id
	ch := make(chan []byte, eventChanSize)
	r.eventSubs[id] = ch
	return EventSub{
		C:      ch,
		Leave:  func() { s.unsubscribeEvents(roomID, id) },
		origin: joinClock(r),
	}, nil
}

func (e EventSub) Rebase(line []byte) []byte {
	return rebasePreviewTs(line, e.origin)
}

func (s *Service) OpenMedia(roomID int) (Media, error) {
	s.mu.Lock()
	r := s.rooms[roomID]
	if r == nil || r.path == "" {
		s.mu.Unlock()
		return Media{}, ErrNotRecording
	}
	id := r.nextSub + 1
	r.nextSub = id
	sub := &mediaSub{id: id, q: newMediaQ(s.mediaMaxQueuedBytes), ext: r.ext}
	r.media[id] = sub
	r.mediaN.Add(1)
	ext := r.ext
	s.mu.Unlock()
	return Media{
		MIME: mediaMIME(ext),
		Body: &chunkReader{q: sub.q, leave: func() { s.unsubscribeMedia(roomID, id) }},
	}, nil
}

func (s *Service) bind(roomID int, path, ext string) *roomState {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[roomID]
	if r == nil {
		r = &roomState{eventSubs: make(map[uint64]chan []byte), media: make(map[uint64]*mediaSub)}
		s.rooms[roomID] = r
	}
	r.path = path
	r.ext = ext
	r.segmentStart = time.Now()
	r.bootstrap = nil
	r.bootstrapReady.Store(false)
	return r
}

func (s *Service) unbind(roomID int, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[roomID]
	if r == nil || r.path != path {
		return
	}
	s.broadcastLocked(r, []byte(`{"type":"segment"}`))
	mediaCount := len(r.media)
	if mediaCount > 0 {
		log.Infof("房间 %d 录制分段结束，关闭 %d 路预览媒体连接（segment）", roomID, mediaCount)
	}
	for id, sub := range r.media {
		sub.kick()
		delete(r.media, id)
	}
	r.mediaN.Store(0)
	r.path = ""
	r.bootstrap = nil
	r.bootstrapReady.Store(false)
	if len(r.eventSubs) == 0 {
		delete(s.rooms, roomID)
	}
}

func (s *Service) unsubscribeEvents(roomID int, id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[roomID]
	if r == nil {
		return
	}
	ch, ok := r.eventSubs[id]
	if !ok {
		return
	}
	delete(r.eventSubs, id)
	close(ch)
	s.maybeDeleteLocked(roomID, r)
}

func (s *Service) unsubscribeMedia(roomID int, id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[roomID]
	if r == nil {
		return
	}
	sub, ok := r.media[id]
	if !ok {
		return
	}
	delete(r.media, id)
	sub.kick()
	if n := r.mediaN.Load(); n > 0 {
		r.mediaN.Store(n - 1)
	}
	s.maybeDeleteLocked(roomID, r)
}

func (s *Service) fanout(roomID int, data []byte) {
	s.mu.Lock()
	r := s.rooms[roomID]
	if r == nil || r.mediaN.Load() == 0 {
		s.mu.Unlock()
		return
	}
	subs := make([]*mediaSub, 0, len(r.media))
	for _, sub := range r.media {
		subs = append(subs, sub)
	}
	boot := r.bootstrap
	s.mu.Unlock()

	var drops []uint64
	for _, sub := range subs {
		if sub.closed.Load() {
			continue
		}
		cp := append([]byte(nil), data...)
		out := sub.join.feed(sub.ext, cp, boot)
		if len(out) == 0 {
			continue
		}
		if !sub.q.TryPush(out) {
			log.Warnf(
				"房间 %d 预览媒体订阅 %d 出站队列已满（overflow）：queued=%d max=%d chunk=%d",
				roomID, sub.id, sub.q.QueuedBytes(), s.mediaMaxQueuedBytes, len(out),
			)
			sub.kick()
			drops = append(drops, sub.id)
		}
	}
	for _, id := range drops {
		s.unsubscribeMedia(roomID, id)
	}
}

func (s *Service) maybeDeleteLocked(roomID int, r *roomState) {
	if r.path == "" && len(r.eventSubs) == 0 && r.mediaN.Load() == 0 {
		delete(s.rooms, roomID)
	}
}

func (s *Service) broadcastLocked(r *roomState, line []byte) {
	for _, ch := range r.eventSubs {
		select {
		case ch <- line:
		default:
		}
	}
}

func (sub *mediaSub) kick() {
	if sub.closed.CompareAndSwap(false, true) {
		sub.q.Close()
	}
}

func mediaMIME(ext string) string {
	switch strings.ToLower(ext) {
	case ".flv":
		return "video/x-flv"
	case ".ts":
		return "video/mp2t"
	case ".fmp4":
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}

func isFtyp(data []byte) bool {
	return len(data) >= 8 && string(data[4:8]) == "ftyp"
}

func joinClock(r *roomState) float64 {
	if r == nil || r.path == "" || r.segmentStart.IsZero() {
		return 0
	}
	secs := time.Since(r.segmentStart).Seconds()
	if secs < 0 {
		return 0
	}
	return secs
}
