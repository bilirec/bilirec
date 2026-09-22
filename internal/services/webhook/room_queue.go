package webhook

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/puzpuzpuz/xsync/v4"
)

const (
	perRoomQueueCap = 32
	pendingWorkCap  = 64
	roomIdleEvict   = 30 * time.Second
)

type roomQueue struct {
	ch         chan envelope
	mu         sync.Mutex
	closed     bool
	scheduled  atomic.Bool
	emptySince time.Time
}

type roomWork struct {
	roomID int
	q      *roomQueue
}

func newRoomQueue() *roomQueue {
	return &roomQueue{
		ch: make(chan envelope, perRoomQueueCap),
	}
}

// trySend enqueues body. retry true means the queue was closed (caller may retry with a new queue).
// queued true means the event was buffered and the worker should be scheduled.
func (q *roomQueue) trySend(roomID int, body envelope) (queued bool, retry bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false, true
	}
	select {
	case q.ch <- body:
		q.emptySince = time.Time{}
		return true, false
	default:
		log.Warnf("webhook 房间 %d 队列已满，丢弃事件 %s", roomID, body.EventType)
		return false, false
	}
}

func (q *roomQueue) isClosed() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.closed
}

func (q *roomQueue) claimSchedule() bool {
	return q.scheduled.CompareAndSwap(false, true)
}

func (q *roomQueue) releaseSchedule() {
	q.scheduled.Store(false)
}

func (s *Service) getOrCreateQueue(roomID int) *roomQueue {
	q := newRoomQueue()
	actual, _ := s.rooms.LoadOrStore(roomID, q)
	return actual
}

func (s *Service) maybeSchedule(roomID int, q *roomQueue) {
	if q.isClosed() || !q.claimSchedule() {
		return
	}
	select {
	case s.pending <- roomWork{roomID: roomID, q: q}:
	case <-s.ctx.Done():
		q.releaseSchedule()
	default:
		q.releaseSchedule()
		log.Warnf("webhook pending 已满，房间 %d 将由 worker 稍后重新调度", roomID)
	}
}

func (s *Service) enqueue(roomID int, body envelope) {
	if s.ctx.Err() != nil {
		return
	}
	for attempt := 0; attempt < 2; attempt++ {
		q := s.getOrCreateQueue(roomID)
		queued, retry := q.trySend(roomID, body)
		if retry {
			continue
		}
		if queued {
			s.maybeSchedule(roomID, q)
		}
		return
	}
	log.Warnf("webhook 房间 %d 入队失败，丢弃事件 %s", roomID, body.EventType)
}

func (s *Service) compareAndDeleteRoom(roomID int, q *roomQueue) bool {
	var deleted bool
	s.rooms.Compute(roomID, func(old *roomQueue, loaded bool) (*roomQueue, xsync.ComputeOp) {
		if !loaded || old != q {
			return old, xsync.CancelOp
		}
		deleted = true
		return nil, xsync.DeleteOp
	})
	return deleted
}

func (s *Service) runWorker() {
	evictTick := time.NewTicker(roomIdleEvict / 2)
	defer evictTick.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case w := <-s.pending:
			s.processRoomWork(w)
		case <-evictTick.C:
			s.rescheduleBackloggedRooms()
			s.evictIdleRooms()
		}
	}
}

func (s *Service) rescheduleBackloggedRooms() {
	s.rooms.Range(func(roomID int, q *roomQueue) bool {
		q.mu.Lock()
		backlog := !q.closed && len(q.ch) > 0
		q.mu.Unlock()
		if backlog {
			s.maybeSchedule(roomID, q)
		}
		return true
	})
}

func (s *Service) processRoomWork(w roomWork) {
	w.q.releaseSchedule()
	if w.q.isClosed() {
		return
	}

	select {
	case body, ok := <-w.q.ch:
		if !ok {
			return
		}
		s.deliver(s.ctx, body)
	default:
		return
	}

	w.q.mu.Lock()
	defer func() {
		w.q.mu.Unlock()
		if !w.q.closed && len(w.q.ch) > 0 {
			s.maybeSchedule(w.roomID, w.q)
		}
	}()
	if w.q.closed {
		return
	}
	if len(w.q.ch) == 0 {
		w.q.emptySince = time.Now()
	}
}

func (s *Service) evictIdleRooms() {
	cutoff := time.Now().Add(-roomIdleEvict)
	s.rooms.Range(func(roomID int, q *roomQueue) bool {
		q.mu.Lock()
		evict := !q.closed && len(q.ch) == 0 && !q.emptySince.IsZero() && !q.emptySince.After(cutoff)
		if evict {
			q.closed = true
		}
		q.mu.Unlock()
		if evict {
			s.compareAndDeleteRoom(roomID, q)
		}
		return true
	})
}
