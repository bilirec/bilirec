package sink

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrNilTransport = errors.New("sink: transport is nil")
	ErrSinkStopped  = errors.New("sink: sink is stopped")
)

type stopRequest struct {
	ctx  context.Context
	done chan error
}

type AsyncBufferedSink struct {
	transport Transport
	options   Options

	ch       chan []byte
	flushReq chan chan struct{}
	stopReq  chan stopRequest
	stopCh   chan struct{}
	done     chan struct{}
	stopped  atomic.Bool

	wg sync.WaitGroup
}

func NewAsyncBufferedSink(transport Transport, opts Options) (*AsyncBufferedSink, error) {
	if transport == nil {
		return nil, ErrNilTransport
	}
	options := normalizeOptions(opts)
	s := &AsyncBufferedSink{
		transport: transport,
		options:   options,
		ch:        make(chan []byte, options.BufferSize),
		flushReq:  make(chan chan struct{}),
		stopReq:   make(chan stopRequest, 1),
		stopCh:    make(chan struct{}),
		done:      make(chan struct{}),
	}
	s.wg.Add(1)
	go s.run()
	return s, nil
}

func (s *AsyncBufferedSink) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if s.stopped.Load() {
		return 0, ErrSinkStopped
	}

	cp := make([]byte, len(p))
	copy(cp, p)

	switch s.options.Overflow {
	case OverflowBlock:
		select {
		case s.ch <- cp:
			if s.options.Hooks.OnQueueBytes != nil {
				s.options.Hooks.OnQueueBytes(len(cp))
			}
		case <-s.stopCh:
			return 0, ErrSinkStopped
		}
	default:
		select {
		case s.ch <- cp:
			if s.options.Hooks.OnQueueBytes != nil {
				s.options.Hooks.OnQueueBytes(len(cp))
			}
		default:
			if s.options.Hooks.OnDropped != nil {
				s.options.Hooks.OnDropped()
			}
		}
	}

	return len(p), nil
}

func (s *AsyncBufferedSink) Sync() error {
	if s.stopped.Load() {
		return ErrSinkStopped
	}
	ack := make(chan struct{})
	select {
	case s.flushReq <- ack:
	case <-s.done:
		return ErrSinkStopped
	}
	select {
	case <-ack:
		return nil
	case <-s.done:
		return ErrSinkStopped
	}
}

func (s *AsyncBufferedSink) Stop(ctx context.Context) error {
	if !s.stopped.CompareAndSwap(false, true) {
		return nil
	}
	close(s.stopCh)
	req := stopRequest{ctx: ctx, done: make(chan error, 1)}
	select {
	case s.stopReq <- req:
	case <-s.done:
		return nil
	}
	select {
	case err := <-req.done:
		s.wg.Wait()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *AsyncBufferedSink) run() {
	defer s.wg.Done()
	defer close(s.done)

	ticker := time.NewTicker(s.options.FlushInterval)
	defer ticker.Stop()

	var batch bytes.Buffer

	flush := func() {
		if batch.Len() == 0 {
			return
		}
		payload := make([]byte, batch.Len())
		copy(payload, batch.Bytes())
		batch.Reset()
		if err := s.transport.Consume(payload); err != nil && s.options.Hooks.OnFailed != nil {
			s.options.Hooks.OnFailed(err)
		}
	}

	appendChunk := func(chunk []byte) {
		if s.options.Hooks.OnQueueBytes != nil {
			s.options.Hooks.OnQueueBytes(-len(chunk))
		}
		if batch.Len() > 0 && batch.Len()+len(chunk) > s.options.BatchBytes {
			flush()
		}
		batch.Write(chunk)
		if batch.Len() >= s.options.BatchBytes {
			flush()
		}
	}

	drain := func() {
		for {
			select {
			case chunk := <-s.ch:
				appendChunk(chunk)
			default:
				return
			}
		}
	}

	for {
		select {
		case chunk := <-s.ch:
			appendChunk(chunk)
		case ack := <-s.flushReq:
			drain()
			flush()
			close(ack)
		case <-ticker.C:
			drain()
			flush()
		case req := <-s.stopReq:
			drain()
			flush()
			req.done <- s.transport.Close(req.ctx)
			return
		}
	}
}
