package recorder

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/backoff"
	"github.com/bilirec/bilirec/pkg/ds"
	"github.com/bilirec/bilirec/pkg/tx"
	"github.com/puzpuzpuz/xsync/v4"
)

func newTestRecorderService(t *testing.T) *Service {
	t.Helper()
	r := &Service{
		recording: xsync.NewMap[int, *Info](),
		cfg: &config.Config{
			OutputDir:               t.TempDir(),
			MaxConcurrentRecordings: 10,
			MaxRecordingHours:       24,
		},
		ctx: context.Background(),
	}
	r.reser = tx.NewPending(
		func(roomId int, pending ds.Set[int]) error {
			if status := r.GetStatus(roomId); status == Recording {
				return ErrRecordingStarted
			}
			if (r.recording.Size() + pending.Size()) > r.cfg.MaxConcurrentRecordings {
				if existing, ok := r.recording.Load(roomId); !ok {
					return ErrMaxConcurrentRecordingsReached
				} else if status := existing.status.Load(); status != recoveringPtr {
					return ErrMaxConcurrentRecordingsReached
				}
			}
			return nil
		},
	)
	return r
}

func TestStart_RejectsRecovering(t *testing.T) {
	r := newTestRecorderService(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	info := &Info{
		ctx:    ctx,
		cancel: cancel,
		backoff: backoff.NewSequence(1 * time.Second),
		room:   &bilibili.LiveRoomInfoDetail{RoomID: 1},
	}
	info.status.Store(recoveringPtr)
	r.recording.Store(1, info)

	if err := r.Start(1); !errors.Is(err, ErrRecordRecovering) {
		t.Fatalf("expected ErrRecordRecovering, got %v", err)
	}
}

func TestStart_RejectsRecording(t *testing.T) {
	r := newTestRecorderService(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	info := &Info{
		ctx:    ctx,
		cancel: cancel,
		backoff: backoff.NewSequence(1 * time.Second),
		room:   &bilibili.LiveRoomInfoDetail{RoomID: 1},
	}
	info.status.Store(recordingPtr)
	r.recording.Store(1, info)

	if err := r.Start(1); !errors.Is(err, ErrRecordingStarted) {
		t.Fatalf("expected ErrRecordingStarted, got %v", err)
	}
}

func TestStart_MapsReservationConflict(t *testing.T) {
	r := newTestRecorderService(t)

	txn := r.reser.Begin()
	if err := txn.Reserve(1); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	defer txn.Abort(1)

	if err := r.Start(1); !errors.Is(err, ErrRecordingPending) {
		t.Fatalf("expected ErrRecordingPending, got %v", err)
	}
}

func TestInternalStart_RespectsSessionContextCancel(t *testing.T) {
	r := newTestRecorderService(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	info := &Info{
		ctx:          ctx,
		cancel:       cancel,
		startOptions: RecordStartOptions{},
		backoff:      backoff.NewSequence(1 * time.Second),
		room:         &bilibili.LiveRoomInfoDetail{RoomID: 1},
	}
	info.status.Store(recoveringPtr)
	r.recording.Store(1, info)

	err := r.internalStart(internalStartParams{
		roomId:  1,
		opts:    info.startOptions,
		ctx:     info.ctx,
		mode:    startModeRecovery,
		session: info,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestSessionReadyForConfirm_RecoveryRequiresLiveSession(t *testing.T) {
	r := newTestRecorderService(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	info := &Info{ctx: ctx, cancel: cancel}
	info.status.Store(recoveringPtr)
	r.recording.Store(1, info)

	other := &Info{ctx: ctx, cancel: cancel}
	if r.sessionReadyForConfirm(internalStartParams{
		roomId:  1,
		ctx:     ctx,
		mode:    startModeRecovery,
		session: other,
	}) {
		t.Fatal("expected false when session pointer mismatches")
	}

	r.recording.Delete(1)
	if r.sessionReadyForConfirm(internalStartParams{
		roomId:  1,
		ctx:     ctx,
		mode:    startModeRecovery,
		session: info,
	}) {
		t.Fatal("expected false when session removed from map")
	}
}

func TestSnapshotStartOptions_CopiesSlice(t *testing.T) {
	orig := RecordStartOptions{
		hasDuration: true,
		duration:    time.Minute,
		streamOptions: []bilibili.GetStreamURLsOption{
			bilibili.WithQn(bilibili.QualitySuper),
		},
	}
	snap := snapshotStartOptions(orig)
	orig.streamOptions = nil

	if len(snap.streamOptions) != 1 {
		t.Fatalf("expected copied stream options, got %d", len(snap.streamOptions))
	}
}

func TestCommitSession_RecoveryPreserveStartTime(t *testing.T) {
	r := newTestRecorderService(t)
	r.cfg.RecordingRecoveryDuration = "preserve"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	originalStart := time.Now().Add(-10 * time.Minute)
	info := &Info{
		ctx:       ctx,
		cancel:    cancel,
		startTime: originalStart,
		backoff:   backoff.NewSequence(1 * time.Second),
		room:      &bilibili.LiveRoomInfoDetail{RoomID: 1},
	}
	info.status.Store(recoveringPtr)
	r.recording.Store(1, info)

	txn := r.reser.Begin()
	now := time.Now()
	roomInfo := &bilibili.LiveRoomInfoDetail{RoomID: 1, LiveStatus: 1}

	got, err := r.commitSession(internalStartParams{
		roomId:  1,
		ctx:     ctx,
		mode:    startModeRecovery,
		session: info,
	}, txn, roomInfo, now, time.Hour)
	if err != nil {
		t.Fatalf("commitSession: %v", err)
	}
	txn.Confirm(1)

	if got.startTime != originalStart {
		t.Fatalf("expected startTime preserved, got %v want %v", got.startTime, originalStart)
	}
}

func TestCommitSession_RecoveryResetStartTime(t *testing.T) {
	r := newTestRecorderService(t)
	r.cfg.RecordingRecoveryDuration = "reset"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	originalStart := time.Now().Add(-10 * time.Minute)
	info := &Info{
		ctx:       ctx,
		cancel:    cancel,
		startTime: originalStart,
		backoff:   backoff.NewSequence(1 * time.Second),
		room:      &bilibili.LiveRoomInfoDetail{RoomID: 1},
	}
	info.status.Store(recoveringPtr)
	r.recording.Store(1, info)

	txn := r.reser.Begin()
	now := time.Now()
	roomInfo := &bilibili.LiveRoomInfoDetail{RoomID: 1, LiveStatus: 1}

	got, err := r.commitSession(internalStartParams{
		roomId:  1,
		ctx:     ctx,
		mode:    startModeRecovery,
		session: info,
	}, txn, roomInfo, now, time.Hour)
	if err != nil {
		t.Fatalf("commitSession: %v", err)
	}
	txn.Confirm(1)

	if !got.startTime.Equal(now) {
		t.Fatalf("expected startTime reset to %v, got %v", now, got.startTime)
	}
}
