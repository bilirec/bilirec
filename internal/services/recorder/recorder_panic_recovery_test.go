package recorder

import (
	"context"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/internal/record_strategies"
	"github.com/bilirec/bilirec/internal/services/preview"
	"github.com/bilirec/bilirec/pkg/backoff"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/pkg/pipeline"
)

// panickingProcessor panics on the Process call matching panicAt, standing in
// for an unexpected bug inside the recording pipeline.
type panickingProcessor struct {
	panicAt int
	calls   int
}

func (p *panickingProcessor) Open(context.Context, logger.Logger) error { return nil }

func (p *panickingProcessor) Process(_ context.Context, _ logger.Logger, data []byte) ([]byte, error) {
	p.calls++
	if p.calls >= p.panicAt {
		panic("pipeline boom")
	}
	return data, nil
}

func (p *panickingProcessor) Close() error { return nil }

// panickingStrategy builds a pipeline whose Process panics on the segment
// matching the BuildPipeline call count; later builds stay clean so the rotate
// loop can move on after a recovered panic.
type panickingStrategy struct {
	buildCalls int
	closed     bool
}

func (s *panickingStrategy) FileExtension() string { return ".flv" }

func (s *panickingStrategy) BuildPipeline(
	_ context.Context,
	_ string,
	_ *record_strategies.RotationState,
	_ record_strategies.PipelineHooks,
) (*pipeline.Pipe[[]byte], error) {
	s.buildCalls++
	boom := &panickingProcessor{panicAt: s.buildCalls}
	return pipeline.New[[]byte](pipeline.NewProcessorInfo("panic-stub", boom)), nil
}

func (s *panickingStrategy) HandleErr(error) record_strategies.ErrHandleResult {
	return record_strategies.ErrHandleResult{Action: record_strategies.ErrActionRotate}
}

func (s *panickingStrategy) Close() error {
	s.closed = true
	return nil
}

// newPanicTestSession wires a recording session the way internalStart would:
// status recording, valid cancel/backoff, and a non-nil StreamObservation so
// rev's per-chunk accounting never dereferences nil.
func newPanicTestSession(r *Service, roomID int, uname string) (*Info, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	info := &Info{
		ctx:     ctx,
		cancel:  cancel,
		backoff: backoff.NewSequence(0),
		room: &bilibili.LiveRoomInfoDetail{
			RoomID:     int64(roomID),
			Uname:      uname,
			Title:      uname,
			LiveStatus: 1,
		},
		startOptions: RecordStartOptions{},
		stats:        &statsObserver{},
		streamObs:    r.m.StreamObservation(roomID, metrics.StreamObservationMeta{}),
	}
	info.status.Store(recordingPtr)
	r.recording.Store(roomID, info)
	return info, cancel
}

// TestRecorder_PanicInPipelineRotatesToNextSegment verifies that a panic inside
// the rev loop (pipeline.Process) is captured by the RecoverPanic defer without
// escaping or killing the goroutine, that the recovery give-up path cleaned the
// session (no leaked pipe, status back to idle), and that a fresh rotate on the
// same strategy succeeds afterwards with an advanced fileTime.
//
// MinDiskSpaceBytes is raised so recover's internalStart fails at the disk
// gate before reaching the live API: the test has no bilibili client, and a
// nil-client panic during unwind would replace the pipeline panic under capture.
func TestRecorder_PanicInPipelineRotatesToNextSegment(t *testing.T) {
	r := newTestRecorderService(t)
	r.preview = preview.NewService(r.cfg)
	r.cfg.MinDiskSpaceBytes = 1 << 50
	r.cfg.MaxRecoveryAttempts = 1

	info, cancel := newPanicTestSession(r, 9001, "panic-stub")
	defer cancel()

	strategy := &panickingStrategy{}
	liveCh := make(chan []byte, 4)
	liveCh <- make([]byte, 16) // one chunk → rev calls pipe.Process → panic
	close(liveCh)

	recovered := make(chan any, 1)
	done := make(chan struct{})
	go func() {
		// Defer order (LIFO): recover → RecoverPanic → close(done). The
		// pipeline panic unwinds through recover's give-up path and is then
		// captured by RecoverPanic; done closes only after the goroutine has
		// fully exited the recording loop.
		defer close(done)
		defer log.RecoverPanic("测试房间 9001 录制循环", func(p any) { recovered <- p })
		defer r.recover(9001)
		_ = r.rotate(9001, liveCh, strategy, info, info.ctx, false)
	}()

	select {
	case p := <-recovered:
		if p != "pipeline boom" {
			t.Fatalf("recovered unexpected panic: %v", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("panic was not recovered")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("rotate goroutine did not exit after recovered panic")
	}

	// Rotation defers ran despite the panic: the strategy was closed.
	if !strategy.closed {
		t.Fatal("rotate unwind should close the strategy")
	}

	// Recovery ran to its give-up path: session removed, no leaked pipe.
	if r.GetStatus(9001) != Idle {
		t.Fatalf("expected status Idle after give-up, got %v", r.GetStatus(9001))
	}
	if _, ok := r.pipes.Load(9001); ok {
		t.Fatal("pipe should be closed and removed after give-up")
	}

	// A fresh rotation on the same strategy works: advance fileTime like
	// commitSession does, then rotate into a new file without panicking.
	info2, cancel2 := newPanicTestSession(r, 9001, "panic-stub")
	defer cancel2()
	info2.fileTime = nextFileTime(info.fileTime, time.Now())

	liveCh2 := make(chan []byte, 4)
	close(liveCh2)
	if err := r.rotate(9001, liveCh2, strategy, info2, info2.ctx, false); err != nil {
		t.Fatalf("post-panic rotate failed: %v", err)
	}
	if strategy.buildCalls != 2 {
		t.Fatalf("expected 2 BuildPipeline calls, got %d", strategy.buildCalls)
	}
	r.Stop(9001)
}

// TestRecorder_RecoverGivesUpCleanlyAfterPanic verifies the recover path after
// a captured pipeline panic: internalStart re-runs, fails deterministically at
// the disk gate (no live API involved), and the session gives up cleanly — the
// recording map and pipes are emptied, status returns to idle, and no
// goroutine residue remains.
func TestRecorder_RecoverGivesUpCleanlyAfterPanic(t *testing.T) {
	r := newTestRecorderService(t)
	r.preview = preview.NewService(r.cfg)
	r.cfg.MinDiskSpaceBytes = 1 << 50
	r.cfg.MaxRecoveryAttempts = 1

	info, cancel := newPanicTestSession(r, 9002, "panic-stub-2")
	defer cancel()

	strategy := &panickingStrategy{}
	liveCh := make(chan []byte, 4)
	liveCh <- make([]byte, 16)
	close(liveCh)

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer log.RecoverPanic("测试房间 9002 录制循环")
		defer r.recover(9002)
		_ = r.rotate(9002, liveCh, strategy, info, info.ctx, false)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("panic recovery loop did not finish")
	}

	if r.GetStatus(9002) != Idle {
		t.Fatalf("expected status Idle after give-up, got %v", r.GetStatus(9002))
	}
	if _, ok := r.recording.Load(9002); ok {
		t.Fatal("session should be removed after give-up")
	}
	if _, ok := r.pipes.Load(9002); ok {
		t.Fatal("pipe should be closed and removed after give-up")
	}
	// The test goroutine survived both the pipeline panic and the give-up
	// publish path: nothing above panicked the test process.
}
