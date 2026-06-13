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
			processor.logger.Errorf("关闭处理器失败：%v", err)
		}
	}
	p.opened.Store(0)
	p.closable.Store(false)
}

func (p *Pipe[T]) process(ctx context.Context, tp *ProcessorInfo[T], item T) (T, error) {
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		if tp.timeout > 0 && elapsed > tp.timeout/2 {
			tp.logger.Warnf("处理器执行耗时过长：%vms", elapsed.Microseconds())
		} else {
			tp.logger.Tracef("processor executed: %vms", elapsed.Microseconds())
		}
	}()
	next, err := p.callWithTimeout(ctx, tp, item)
	if err != nil {
		switch tp.errorStrategy {
		case StopOnError:
			return item, err
		case ReturnNextOnError:
			return next, err
		case ContinueOnError:
			tp.logger.Warnf("处理器 %s 出错但继续执行：%v", tp.name, err)
			return item, nil
		case RetryOnError:
			for range tp.maxRetries {
				tp.logger.Warnf("处理器 %s 因错误重试：%v", tp.name, err)
				select {
				case <-time.After(tp.retryInterval):
					next, retryErr := p.callWithTimeout(ctx, tp, item)
					if retryErr == nil {
						tp.logger.Infof("处理器 %s 重试成功", tp.name)
						return next, nil
					}
					err = retryErr
				case <-ctx.Done():
					return item, ctx.Err()
				}
			}
			tp.logger.Errorf("处理器 %s 在重试 %d 次后仍失败", tp.name, tp.maxRetries)
			return item, err
		}
	}
	return next, err
}

func (p *Pipe[T]) callWithTimeout(ctx context.Context, tp *ProcessorInfo[T], item T) (T, error) {
	if tp.timeout <= 0 {
		return tp.process(ctx, item)
	}
	if deadline, ok := ctx.Deadline(); ok {
		if time.Until(deadline) <= tp.timeout {
			return tp.process(ctx, item)
		}
	}
	c, cancel := context.WithTimeout(ctx, tp.timeout)
	defer cancel()
	return tp.process(c, item)
}
