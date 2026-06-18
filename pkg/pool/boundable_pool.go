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
	item, ok := p.reclaim(item)
	if !ok {
		return
	}
	if p.mode == BufferPoolModeBounded {
		select {
		case p.bounded <- item:
		default:
			// Queue is full; let item be garbage collected.
		}
		return
	}
	p.soft.Put(item)
}
