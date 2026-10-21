package preview

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/pipeline"
)

const eventChanSize = 256

var (
	ErrNotRecording = errors.New("该房间当前没有可预览的录制")
)

type Service struct {
	mu    sync.Mutex
	rooms map[int]*roomState
}

func NewService() *Service {
	return &Service{rooms: make(map[int]*roomState)}
}

func (s *Service) Tap(roomID int, path, ext string) *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"preview-fanout",
		&fanoutProcessor{svc: s, roomID: roomID, path: path, ext: ext},
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

func (s *Service) Subscribe(roomID int) (<-chan []byte, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[roomID]
	if r == nil || r.path == "" {
		return nil, nil, ErrNotRecording
	}
	id := r.nextSub + 1
	r.nextSub = id
	ch := make(chan []byte, eventChanSize)
	r.eventSubs[id] = ch
	return ch, func() { s.unsubscribeEvents(roomID, id) }, nil
}

type Media struct {
	MIME   string
	Status int
	Body   io.ReadCloser
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
	sub := &mediaSub{id: id, ch: make(chan []byte, 1), done: make(chan struct{}), ext: r.ext}
	if r.media == nil {
		r.media = make(map[uint64]*mediaSub)
	}
	r.media[id] = sub
	r.mediaN.Add(1)
	ext := r.ext
	s.mu.Unlock()
	return Media{
		MIME:   mediaMIME(ext),
		Status: http.StatusOK,
		Body:   &chunkReader{ch: sub.ch, done: sub.done, leave: func() { s.unsubscribeMedia(roomID, id) }},
	}, nil
}

// LiveJoinClock is seconds since this recording file opened. SSE catchup
// subtracts it so overlays line up with a player that starts at the live edge.
func (s *Service) LiveJoinClock(roomID int) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[roomID]
	if r == nil || r.path == "" || r.segmentStart.IsZero() {
		return 0
	}
	secs := time.Since(r.segmentStart).Seconds()
	if secs < 0 {
		return 0
	}
	return secs
}

type roomState struct {
	path         string
	ext          string
	segmentStart time.Time

	bootstrapReady atomic.Bool
	bootstrap      []byte
	det            *flv.HeaderChangeDetector

	mediaN atomic.Int32
	media  map[uint64]*mediaSub

	eventSubs map[uint64]chan []byte
	nextSub   uint64
}

type mediaSub struct {
	id     uint64
	ch     chan []byte
	done   chan struct{}
	closed atomic.Bool
	ext    string
	join   joinFilter
}

func (s *Service) bind(roomID int, path, ext string) *roomState {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[roomID]
	if r == nil {
		r = &roomState{eventSubs: make(map[uint64]chan []byte), media: make(map[uint64]*mediaSub)}
		s.rooms[roomID] = r
	}
	if r.det != nil {
		r.det.Close()
		r.det = nil
	}
	r.path = path
	r.ext = ext
	r.segmentStart = time.Now()
	r.bootstrap = nil
	r.bootstrapReady.Store(false)
	if ext == ".flv" {
		r.det = flv.NewHeaderChangeDetector()
	}
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
	for id, sub := range r.media {
		sub.kick()
		delete(r.media, id)
	}
	r.mediaN.Store(0)
	r.path = ""
	r.bootstrap = nil
	r.bootstrapReady.Store(false)
	if r.det != nil {
		r.det.Close()
		r.det = nil
	}
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
	if r == nil {
		s.mu.Unlock()
		return
	}
	if !r.bootstrapReady.Load() {
		s.captureBootstrapLocked(r, data)
	}
	if r.mediaN.Load() == 0 {
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
		select {
		case <-sub.done:
			drops = append(drops, sub.id)
		case sub.ch <- out:
		default:
			sub.kick()
			drops = append(drops, sub.id)
		}
	}
	for _, id := range drops {
		s.unsubscribeMedia(roomID, id)
	}
}

func (s *Service) captureBootstrapLocked(r *roomState, data []byte) {
	if r.bootstrapReady.Load() {
		return
	}
	switch r.ext {
	case ".flv":
		if r.det == nil {
			r.det = flv.NewHeaderChangeDetector()
		}
		_ = r.det.DetectChange(data)
		vTag := flv.ZeroTagTimestamp(r.det.LastVideoTag())
		if len(vTag) == 0 {
			return
		}
		prefix := flv.NewFileHeaderBytes()
		prefix = append(prefix, vTag...)
		if aTag := flv.ZeroTagTimestamp(r.det.LastAudioTag()); len(aTag) > 0 {
			prefix = append(prefix, aTag...)
		}
		r.bootstrap = prefix
		r.bootstrapReady.Store(true)
		r.det.Close()
		r.det = nil
	case ".fmp4":
		if isFtyp(data) {
			r.bootstrap = append([]byte(nil), data...)
			r.bootstrapReady.Store(true)
		}
	default:
		r.bootstrapReady.Store(true)
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

func (sub *mediaSub) kick() {
	if sub.closed.CompareAndSwap(false, true) {
		close(sub.done)
	}
}
