package pipeline

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("pkg", "pipeline")

type Pipe[T any] struct {
	closable   atomic.Bool
	opened     atomic.Int32
	processors []*ProcessorInfo[T]
}

func New[T any](processors ...*ProcessorInfo[T]) *Pipe[T] {
	return &Pipe[T]{
		processors: processors,
	}
}

func (p *Pipe[T]) AddProcessors(processors ...*ProcessorInfo[T]) {
	p.processors = append(p.processors, processors...)
}

func (p *Pipe[T]) Process(ctx context.Context, item T) (T, error) {
	var currentItem T = item
	for _, processor := range p.processors {
		select {
		case <-ctx.Done():
			return currentItem, ctx.Err()
		default:
			var err error
			currentItem, err = p.process(ctx, processor, currentItem)
			if err != nil {
				return currentItem, err
			}
		}
	}
	return currentItem, nil
}

func (p *Pipe[T]) Open(ctx context.Context) error {
	var opened int32
	for _, processor := range p.processors {
		if err := processor.processor.Open(ctx, processor.logger); err != nil {
			if opened > 0 {
				p.closable.Store(true)
				p.opened.Store(opened)
				p.closeOpened(int(opened))
			}
			return err
		}
		opened++
	}
	if opened > 0 {
		p.opened.Store(opened)
		p.closable.Store(true)
	}
	return nil
}

func (p *Pipe[T]) Close() {
	if !p.closable.Load() {
		return
	}
	p.closeOpened(int(p.opened.Load()))
}

func (p *Pipe[T]) closeOpened(count int) {
	if count <= 0 {
		p.opened.Store(0)
		p.closable.Store(false)
		return
	}
	if count > len(p.processors) {
		count = len(p.processors)
	}
	for i := count - 1; i >= 0; i-- {
		processor := p.processors[i]
		if err := processor.close(); err != nil {
			processor.logger.Errorf("error closing processor: %v", err)
		}
	}
	p.opened.Store(0)
	p.closable.Store(false)
}

func (p *Pipe[T]) process(ctx context.Context, tp *ProcessorInfo[T], item T) (T, error) {
	start := time.Now()
	c, cancel := context.WithTimeout(ctx, tp.timeout)
	defer cancel()
	defer func() {
		elapsed := time.Since(start)
		if elapsed > tp.timeout/2 {
			tp.logger.Warnf("processor took too long to execute: %vms", elapsed.Microseconds())
		} else {
			tp.logger.Tracef("processor executed: %vms", elapsed.Microseconds())
		}
	}()
	next, err := tp.process(c, item)
	if err != nil {
		switch tp.errorStrategy {
		case StopOnError:
			return item, err
		case ReturnNextOnError:
			return next, err
		case ContinueOnError:
			tp.logger.Warnf("continuing despite error in processor %s: %v", tp.name, err)
			return item, nil
		case RetryOnError:
			for range tp.maxRetries {
				tp.logger.Warnf("retrying processor %s due to error: %v", tp.name, err)
				select {
				case <-time.After(tp.retryInterval):
					c, cancel = context.WithTimeout(ctx, tp.timeout)
					next, retryErr := tp.process(c, item)
					cancel()
					if retryErr == nil {
						tp.logger.Infof("processor %s succeeded on retry", tp.name)
						return next, nil
					}
					err = retryErr
				case <-ctx.Done():
					return item, ctx.Err()
				}
			}
			tp.logger.Errorf("processor %s failed after %d retries", tp.name, tp.maxRetries)
			return item, err
		}
	}
	return next, err
}
