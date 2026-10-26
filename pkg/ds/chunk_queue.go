package ds

import "sync"

type chunkSlot[T any] struct {
	item   T
	nbytes int
}

// ChunkQueue is a bounded FIFO queue measured by a caller-supplied size function.
type ChunkQueue[T any] struct {
	mu       sync.Mutex
	cond     *sync.Cond
	chunks   []chunkSlot[T]
	queued   int
	maxBytes int
	closed   bool

	allowOversizeWhenEmpty bool
	size                   func(T) int
}

type chunkQueueConfig struct {
	allowOversizeWhenEmpty bool
}

// ChunkQueueOption configures admission policy for NewChunkQueue.
type ChunkQueueOption func(*chunkQueueConfig)

// AllowOversizeWhenEmpty admits a single chunk larger than maxBytes when the queue is empty.
func AllowOversizeWhenEmpty() ChunkQueueOption {
	return func(c *chunkQueueConfig) {
		c.allowOversizeWhenEmpty = true
	}
}

// NewChunkQueue creates a queue with the given byte budget. size returns the byte cost of each item.
func NewChunkQueue[T any](maxBytes int, size func(T) int, opts ...ChunkQueueOption) *ChunkQueue[T] {
	cfg := chunkQueueConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	q := &ChunkQueue[T]{
		maxBytes:               maxBytes,
		size:                   size,
		allowOversizeWhenEmpty: cfg.allowOversizeWhenEmpty,
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// QueuedBytes returns the sum of admitted item sizes.
func (q *ChunkQueue[T]) QueuedBytes() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.queued
}

// CanTake reports whether a chunk of n bytes may be admitted without enqueueing.
func (q *ChunkQueue[T]) CanTake(n int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.canAdmit(n)
}

// TryPush admits item when policy allows. Returns false when closed or over budget.
func (q *ChunkQueue[T]) TryPush(item T) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	n := q.size(item)
	if !q.canAdmit(n) {
		return false
	}
	q.pushSlot(chunkSlot[T]{item: item, nbytes: n})
	return true
}

// Push is TryPush with the same semantics.
func (q *ChunkQueue[T]) Push(item T) bool {
	return q.TryPush(item)
}

// Pop blocks until an item is available or the queue is closed and drained.
func (q *ChunkQueue[T]) Pop() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.chunks) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.chunks) == 0 {
		var zero T
		return zero, false
	}
	slot := q.chunks[0]
	q.chunks[0] = chunkSlot[T]{}
	q.chunks = q.chunks[1:]
	q.queued -= slot.nbytes
	return slot.item, true
}

// Close unblocks waiters; further pushes are rejected.
func (q *ChunkQueue[T]) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	q.cond.Broadcast()
}

func (q *ChunkQueue[T]) canAdmit(n int) bool {
	if q.closed || n <= 0 {
		return false
	}
	if q.allowOversizeWhenEmpty && q.queued == 0 {
		return true
	}
	return q.queued+n <= q.maxBytes
}

func (q *ChunkQueue[T]) pushSlot(slot chunkSlot[T]) {
	q.chunks = append(q.chunks, slot)
	q.queued += slot.nbytes
	q.cond.Signal()
}
