package pipeline_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/pkg/pipeline"
)

var errOpenFailed = errors.New("open failed")

type testProcessor struct {
	name string

	failOpen bool
	delta    int

	mu           sync.Mutex
	opened       bool
	openCalls    int
	processCalls int
	closeCalls   int

	closeOrder *[]string
}

func (p *testProcessor) Open(ctx context.Context, log logger.Logger) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.openCalls++
	if p.failOpen {
		return errOpenFailed
	}
	p.opened = true
	return nil
}

func (p *testProcessor) Process(ctx context.Context, log logger.Logger, item int) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.processCalls++
	return item + p.delta, nil
}

func (p *testProcessor) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeCalls++
	if p.closeOrder != nil {
		*p.closeOrder = append(*p.closeOrder, p.name)
	}
	return nil
}

func TestPipeCloseBeforeOpenNoop(t *testing.T) {
	p1 := &testProcessor{name: "p1", delta: 1}
	pipe := pipeline.New(pipeline.NewProcessorInfo("p1", p1))

	pipe.Close()

	if p1.closeCalls != 0 {
		t.Fatalf("expected closeCalls=0 before open, got %d", p1.closeCalls)
	}
}

func TestPipeOpenCloseIdempotentAndReverseOrder(t *testing.T) {
	order := []string{}
	p1 := &testProcessor{name: "p1", delta: 1, closeOrder: &order}
	p2 := &testProcessor{name: "p2", delta: 2, closeOrder: &order}
	p3 := &testProcessor{name: "p3", delta: 3, closeOrder: &order}

	pipe := pipeline.New(
		pipeline.NewProcessorInfo("p1", p1),
		pipeline.NewProcessorInfo("p2", p2),
		pipeline.NewProcessorInfo("p3", p3),
	)

	if err := pipe.Open(context.Background()); err != nil {
		t.Fatalf("open failed: %v", err)
	}

	pipe.Close()
	pipe.Close()

	if p1.closeCalls != 1 || p2.closeCalls != 1 || p3.closeCalls != 1 {
		t.Fatalf("expected each processor to close once, got p1=%d p2=%d p3=%d", p1.closeCalls, p2.closeCalls, p3.closeCalls)
	}

	if len(order) != 3 {
		t.Fatalf("expected 3 close order entries, got %d", len(order))
	}
	if order[0] != "p3" || order[1] != "p2" || order[2] != "p1" {
		t.Fatalf("expected reverse close order [p3 p2 p1], got %v", order)
	}
}

func TestPipeOpenFailureRollsBackOpenedProcessors(t *testing.T) {
	order := []string{}
	p1 := &testProcessor{name: "p1", delta: 1, closeOrder: &order}
	p2 := &testProcessor{name: "p2", delta: 2, closeOrder: &order}
	p3 := &testProcessor{name: "p3", failOpen: true, closeOrder: &order}

	pipe := pipeline.New(
		pipeline.NewProcessorInfo("p1", p1),
		pipeline.NewProcessorInfo("p2", p2),
		pipeline.NewProcessorInfo("p3", p3),
	)

	err := pipe.Open(context.Background())
	if !errors.Is(err, errOpenFailed) {
		t.Fatalf("expected open error %v, got %v", errOpenFailed, err)
	}

	if p1.closeCalls != 1 || p2.closeCalls != 1 {
		t.Fatalf("expected opened processors to be rolled back once, got p1=%d p2=%d", p1.closeCalls, p2.closeCalls)
	}
	if p3.closeCalls != 0 {
		t.Fatalf("expected failed-open processor not to be closed, got %d", p3.closeCalls)
	}

	if len(order) != 2 || order[0] != "p2" || order[1] != "p1" {
		t.Fatalf("expected rollback close order [p2 p1], got %v", order)
	}

	pipe.Close()
	if p1.closeCalls != 1 || p2.closeCalls != 1 || p3.closeCalls != 0 {
		t.Fatalf("expected close after rollback to be noop, got p1=%d p2=%d p3=%d", p1.closeCalls, p2.closeCalls, p3.closeCalls)
	}
}

func TestPipeProcessAfterCloseReturnsClosedPipe(t *testing.T) {
	p1 := &testProcessor{name: "p1", delta: 1}
	p2 := &testProcessor{name: "p2", delta: 2}

	pipe := pipeline.New(
		pipeline.NewProcessorInfo("p1", p1),
		pipeline.NewProcessorInfo("p2", p2),
	)

	if err := pipe.Open(context.Background()); err != nil {
		t.Fatalf("open failed: %v", err)
	}
	pipe.Close()

	_, err := pipe.Process(context.Background(), 10)
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("expected %v, got %v", io.ErrClosedPipe, err)
	}

	if p1.processCalls != 0 || p2.processCalls != 0 {
		t.Fatalf("expected no processor Process calls after close, got p1=%d p2=%d", p1.processCalls, p2.processCalls)
	}
}
