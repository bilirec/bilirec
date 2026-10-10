package pool

import "sync"

type boundablePool[T any] struct {
	mode    BufferPoolMode
	soft    sync.Pool
	bounded chan T
	newItem func() T
	ready   func(T) T
	reclaim func(T) (T, bool)
}

func newBoundablePool[T any](
	cfg PoolBoundedConfig,
	newItem func() T,
	ready func(T) T,
	reclaim func(T) (T, bool),
) *boundablePool[T] {
	s := &boundablePool[T]{
		mode:    cfg.Mode,
		newItem: newItem,
		ready:   ready,
		reclaim: reclaim,
	}
	if cfg.Mode == BufferPoolModeBounded {
		s.bounded = make(chan T, boundableCapacity(cfg))
	} else {
		s.soft = sync.Pool{New: func() any { return newItem() }}
	}
	return s
}

func (p *boundablePool[T]) get() T {
	if p.mode == BufferPoolModeBounded {
		select {
		case item := <-p.bounded:
			return p.ready(item)
		default:
			return p.ready(p.newItem())
		}
	}
	return p.ready(p.soft.Get().(T))
}

func (p *boundablePool[T]) put(item T) {
	p.tryPut(item)
}

// tryGet returns a pooled item when one is available without allocating.
func (p *boundablePool[T]) tryGet() (T, bool) {
	if p.mode == BufferPoolModeBounded {
		select {
		case item := <-p.bounded:
			return p.ready(item), true
		default:
			var zero T
			return zero, false
		}
	}
	var zero T
	return zero, false
}

// tryPut stores item in the pool when there is capacity. Returns false if the item is rejected or the queue is full.
func (p *boundablePool[T]) tryPut(item T) bool {
	item, ok := p.reclaim(item)
	if !ok {
		return false
	}
	if p.mode == BufferPoolModeBounded {
		select {
		case p.bounded <- item:
			return true
		default:
			return false
		}
	}
	p.soft.Put(item)
	return true
}

// drainBounded removes all items waiting in the bounded queue.
func (p *boundablePool[T]) drainBounded() {
	if p.mode != BufferPoolModeBounded {
		return
	}
	for {
		select {
		case <-p.bounded:
		default:
			return
		}
	}
}
