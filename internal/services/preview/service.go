package preview

import (
	"errors"
	"sync"
	"time"
)

const eventChanSize = 256

var (
	ErrNotRecording        = errors.New("该房间当前没有可预览的录制")
	ErrUnsupportedExt      = errors.New("当前流格式不支持该预览请求")
	ErrSegmentMissing      = errors.New("分片尚未写入磁盘")
	ErrRangeNotSatisfiable = errors.New("请求的范围无法满足")
)

type Service struct {
	mu    sync.Mutex
	rooms map[int]*roomState
}

func NewService() *Service {
	return &Service{rooms: make(map[int]*roomState)}
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
	r.viewers++
	if !r.started {
		r.previewStart = r.offset
		r.started = true
	}
	id := r.nextSub + 1
	r.nextSub = id
	ch := make(chan []byte, eventChanSize)
	r.eventSubs[id] = ch
	return ch, func() {
		s.leave(roomID)
		s.unsubscribe(roomID, id)
	}, nil
}

type roomState struct {
	path   string
	ext    string
	offset int64
	gen    uint64

	viewers      int
	previewStart int64
	started      bool
	lastNote     time.Time
	initOffset   int64
	initSize     int64
	hasInit      bool
	segs         []segment

	eventSubs map[uint64]chan []byte
	nextSub   uint64
}

type segment struct {
	offset int64
	size   int
	durSec float64
}

type roomView struct {
	path, ext            string
	offset, previewStart int64
	gen                  uint64
	segs                 []segment
	initOffset, initSize int64
	hasInit              bool
}

func (s *Service) bind(roomID int, path, ext string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[roomID]
	if r == nil {
		r = &roomState{eventSubs: make(map[uint64]chan []byte)}
		s.rooms[roomID] = r
	}
	r.path = path
	r.ext = ext
	r.offset = 0
	r.gen++
	r.segs = r.segs[:0]
	r.hasInit = false
	r.initOffset = 0
	r.initSize = 0
	r.lastNote = time.Time{}
	if r.viewers > 0 {
		r.previewStart = 0
		r.started = true
	} else {
		r.started = false
		r.previewStart = 0
	}
}

func (s *Service) unbind(roomID int, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[roomID]
	if r == nil || r.path != path {
		return
	}
	s.broadcastLocked(r, []byte(`{"type":"segment"}`))
	r.path = ""
	r.segs = r.segs[:0]
	r.hasInit = false
	r.started = false
	r.gen++
	if r.viewers == 0 && len(r.eventSubs) == 0 {
		delete(s.rooms, roomID)
	}
}

// observe records that n bytes were accepted by the writer. When nobody is
// previewing this is one integer add. HLS segment boundaries are stored only
// while viewers > 0; payload is never copied.
func (s *Service) observe(roomID int, n int, indexHLS, isInit bool) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[roomID]
	if r == nil {
		return
	}
	r.offset += int64(n)
	if r.viewers == 0 || !indexHLS {
		return
	}
	end := r.offset
	if !r.started {
		r.previewStart = end - int64(n)
		if r.previewStart < 0 {
			r.previewStart = 0
		}
		r.started = true
	}
	now := time.Now()
	if isInit {
		r.initOffset = end - int64(n)
		r.initSize = int64(n)
		r.hasInit = true
		r.lastNote = now
		return
	}
	dur := 2.0
	if !r.lastNote.IsZero() {
		dur = now.Sub(r.lastNote).Seconds()
		if dur < 0.2 {
			dur = 0.2
		}
		if dur > 10 {
			dur = 10
		}
	}
	r.lastNote = now
	r.segs = append(r.segs, segment{offset: end - int64(n), size: n, durSec: dur})
}

func (s *Service) leave(roomID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[roomID]
	if r == nil {
		return
	}
	if r.viewers > 0 {
		r.viewers--
	}
	if r.viewers == 0 {
		r.segs = r.segs[:0]
		r.started = false
		r.hasInit = false
		r.previewStart = 0
		if r.path == "" && len(r.eventSubs) == 0 {
			delete(s.rooms, roomID)
		}
	}
}

func (s *Service) unsubscribe(roomID int, id uint64) {
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
	if r.viewers == 0 && r.path == "" && len(r.eventSubs) == 0 {
		delete(s.rooms, roomID)
	}
}

func (s *Service) roomView(roomID int) (roomView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[roomID]
	if r == nil {
		return roomView{}, false
	}
	out := make([]segment, len(r.segs))
	copy(out, r.segs)
	return roomView{
		path:         r.path,
		ext:          r.ext,
		offset:       r.offset,
		previewStart: r.previewStart,
		gen:          r.gen,
		segs:         out,
		initOffset:   r.initOffset,
		initSize:     r.initSize,
		hasInit:      r.hasInit,
	}, true
}

// LiveJoinClock is the recording-timeline second that becomes t=0 for a
// late FLV preview. SSE catchup and live events subtract this so overlays
// stay aligned with the rebased media clock. Non-FLV (or a failed join)
// returns 0 and leaves timestamps unchanged.
func (s *Service) LiveJoinClock(roomID int) float64 {
	v, ok := s.roomView(roomID)
	if !ok || v.ext != ".flv" || v.path == "" {
		return 0
	}
	join, ok := flvLiveJoin(v.path)
	if !ok {
		return 0
	}
	return float64(join.baseTS) / 1000
}

func (s *Service) generation(roomID int) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[roomID]
	if r == nil {
		return 0
	}
	return r.gen
}

func (s *Service) broadcastLocked(r *roomState, line []byte) {
	for _, ch := range r.eventSubs {
		select {
		case ch <- line:
		default:
		}
	}
}
