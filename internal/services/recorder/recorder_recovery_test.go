package recorder

import (
	"context"
	"errors"
	"os"
	"strings"
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
	originalFile := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	info := &Info{
		ctx:       ctx,
		cancel:    cancel,
		startTime: originalStart,
		fileTime:  originalFile,
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
	wantFile := nextFileTime(originalFile, now)
	if !got.fileTime.Equal(wantFile) {
		t.Fatalf("expected fileTime %v, got %v", wantFile, got.fileTime)
	}
}

func TestCommitSession_RecoveryResetStartTime(t *testing.T) {
	r := newTestRecorderService(t)
	r.cfg.RecordingRecoveryDuration = "reset"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	originalStart := time.Now().Add(-10 * time.Minute)
	originalFile := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	info := &Info{
		ctx:       ctx,
		cancel:    cancel,
		startTime: originalStart,
		fileTime:  originalFile,
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
	wantFile := nextFileTime(originalFile, now)
	if !got.fileTime.Equal(wantFile) {
		t.Fatalf("expected fileTime %v, got %v", wantFile, got.fileTime)
	}
}

func TestNextFileTime_SameSecondAdvances(t *testing.T) {
	prev := time.Date(2026, 7, 11, 16, 4, 5, 123, time.Local)
	now := time.Date(2026, 7, 11, 16, 4, 5, 999, time.Local)
	got := nextFileTime(prev, now)
	want := prev.Truncate(time.Second).Add(time.Second)
	if !got.Equal(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestNextFileTime_AfterPrevUsesNow(t *testing.T) {
	prev := time.Date(2026, 7, 11, 16, 4, 5, 0, time.Local)
	now := time.Date(2026, 7, 11, 16, 4, 7, 500, time.Local)
	got := nextFileTime(prev, now)
	want := now.Truncate(time.Second)
	if !got.Equal(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestRotateFilePath_UsesFileTimeNotStartTime(t *testing.T) {
	r := newTestRecorderService(t)
	start := time.Date(2026, 7, 11, 16, 0, 0, 0, time.Local)
	file := time.Date(2026, 7, 11, 16, 4, 5, 0, time.Local)
	info := &Info{
		startTime: start,
		fileTime:  file,
		room: &bilibili.LiveRoomInfoDetail{
			RoomID: 12345,
			Uname:  "testuser",
			Title:  "直播标题",
		},
	}

	path, err := r.rotateFilePath(info, 0, ".flv")
	if err != nil {
		t.Fatalf("rotateFilePath: %v", err)
	}
	if !strings.Contains(path, "20260711_160405") {
		t.Fatalf("expected path to use fileTime stamp, got %q", path)
	}
	if strings.Contains(path, "20260711_160000") {
		t.Fatalf("path unexpectedly used startTime, got %q", path)
	}
}

func TestRotateFilePath_RecoveryPreserveNoOverwrite(t *testing.T) {
	r := newTestRecorderService(t)
	t0 := time.Date(2026, 7, 11, 16, 4, 5, 0, time.Local)
	info := &Info{
		startTime: t0,
		fileTime:  t0,
		room: &bilibili.LiveRoomInfoDetail{
			RoomID: 12345,
			Uname:  "testuser",
			Title:  "直播标题",
		},
	}

	path0, err := r.rotateFilePath(info, 0, ".flv")
	if err != nil {
		t.Fatalf("rotateFilePath: %v", err)
	}
	if err := os.WriteFile(path0, []byte("original"), 0o644); err != nil {
		t.Fatalf("write original file: %v", err)
	}

	info.fileTime = nextFileTime(info.fileTime, t0) // same-second recovery
	path1, err := r.rotateFilePath(info, 0, ".flv")
	if err != nil {
		t.Fatalf("rotateFilePath after recovery: %v", err)
	}
	if path0 == path1 {
		t.Fatalf("paths collided: %q", path0)
	}

	f, err := os.Create(path1)
	if err != nil {
		t.Fatalf("create recovered file: %v", err)
	}
	if _, err := f.WriteString("recovered"); err != nil {
		_ = f.Close()
		t.Fatalf("write recovered file: %v", err)
	}
	_ = f.Close()

	original, err := os.ReadFile(path0)
	if err != nil {
		t.Fatalf("read original file: %v", err)
	}
	if string(original) != "original" {
		t.Fatalf("original file overwritten, content=%q", original)
	}

	recovered, err := os.ReadFile(path1)
	if err != nil {
		t.Fatalf("read recovered file: %v", err)
	}
	if string(recovered) != "recovered" {
		t.Fatalf("recovered file content=%q", recovered)
	}
}
