package pipeline

import (
	"context"
	"io"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

type ErrorStrategy int

const (
	StopOnError ErrorStrategy = iota
	ReturnNextOnError
	ContinueOnError
	RetryOnError
)

type Processor[T any] interface {
	Open(ctx context.Context, log *logrus.Entry) error
	Process(ctx context.Context, log *logrus.Entry, item T) (T, error)
	io.Closer
}

type ProcessorInfo[T any] struct {
	name      string
	processor Processor[T]
	logger    *logrus.Entry

	errorStrategy ErrorStrategy
	maxRetries    int32
	retryInterval time.Duration
	timeout       time.Duration
	closed        atomic.Bool
}

type ProcessorOption[T any] func(*ProcessorInfo[T])

func NewProcessorInfo[T any](name string, processor Processor[T], options ...ProcessorOption[T]) *ProcessorInfo[T] {
	pro := &ProcessorInfo[T]{
		name:          name,
		processor:     processor,
		errorStrategy: StopOnError,
		maxRetries:    3,
		retryInterval: 1 * time.Second,
		timeout:       10 * time.Second,
		logger:        logger.WithField("processor", name),
	}
	for _, option := range options {
		option(pro)
	}
	return pro
}

func (p *ProcessorInfo[T]) process(ctx context.Context, item T) (T, error) {
	if p.closed.Load() {
		return item, io.ErrClosedPipe
	}
	return p.processor.Process(ctx, p.logger, item)
}

func (p *ProcessorInfo[T]) close() error {
	if !p.closed.CompareAndSwap(false, true) {
		return nil
	}
	return p.processor.Close()
}

func WithErrorStrategy[T any](strategy ErrorStrategy) ProcessorOption[T] {
	return func(pi *ProcessorInfo[T]) {
		pi.errorStrategy = strategy
	}
}

func WithRetryOptions[T any](maxRetries int32, retryInterval time.Duration) ProcessorOption[T] {
	return func(pi *ProcessorInfo[T]) {
		pi.maxRetries = maxRetries
		if retryInterval > 0 {
			pi.retryInterval = retryInterval
		} else {
			pi.logger.Warnf("invalid specified retry interval %v for processor %s, using default", retryInterval, pi.name)
		}
	}
}

func WithTimeout[T any](timeout time.Duration) ProcessorOption[T] {
	return func(pi *ProcessorInfo[T]) {
		if timeout > 0 {
			pi.timeout = timeout
		} else {
			pi.logger.Warnf("invalid specified timeout %v for processor %s, using default", timeout, pi.name)
		}
	}
}

func WithLogger[T any](logger *logrus.Entry) ProcessorOption[T] {
	return func(pi *ProcessorInfo[T]) {
		pi.logger = logger
	}
}
