package recorder

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/internal/services/convert"
	"github.com/bilirec/bilirec/internal/services/danmaku"
	"github.com/bilirec/bilirec/internal/services/notify"
	"github.com/bilirec/bilirec/internal/services/path"
	"github.com/bilirec/bilirec/internal/services/room"
	"github.com/bilirec/bilirec/internal/services/stream"
	"github.com/bilirec/bilirec/internal/services/webhook"
	"github.com/bilirec/bilirec/pkg/tx"
	"github.com/bilirec/bilirec/utils"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

const (
	lowDiskTestUname   = "lowdisk-anchor"
	lowDiskTestRoomID  = 88001
	lowDiskOtherRoomID = 88002

	// Padded segment size used with syncLowDiskThreshold: each deleted fixture should
	// free about this many bytes on the output mount (see syncLowDiskThreshold).
	lowDiskTestMediaSize = 512 * 1024
)

// Serializes low-disk integration tests. usage.Free is mount-wide; other packages
// in go test ./... can shift it between threshold sync and ensureDiskSpace.
var lowDiskIntegrationMu sync.Mutex

// Integration tests for delete_oldest_on_low_disk boot the recorder service graph via
// fxtest (same providers as recording integration tests) and exercise Start()/internalStart.
func TestIntegration_LowDisk_RejectsWhenPurgeDisabled(t *testing.T) {
	lowDiskIntegrationMu.Lock()
	defer lowDiskIntegrationMu.Unlock()

	r := newLowDiskFxRecorder(t)

	keptPath := writeRoomMediaFile(t, r.cfg.OutputDir, lowDiskTestUname, lowDiskTestRoomID,
		"seg-20200101_000000.flv", []byte("old-segment"))
	syncLowDiskThreshold(t, r.cfg.OutputDir, r.cfg)

	err := r.ensureDiskSpace(log, internalStartParams{roomId: lowDiskTestRoomID})
	if !errors.Is(err, ErrInsufficientDiskSpace) {
		t.Fatalf("expected ErrInsufficientDiskSpace, got %v", err)
	}
	if _, statErr := os.Stat(keptPath); statErr != nil {
		t.Fatalf("expected media file to remain when purge is disabled: %v", statErr)
	}
}

func TestIntegration_LowDisk_DeletesOldestInRoomByFilename(t *testing.T) {
	lowDiskIntegrationMu.Lock()
	defer lowDiskIntegrationMu.Unlock()

	r := newLowDiskFxRecorder(t)

	oldPath := writeRoomMediaFile(t, r.cfg.OutputDir, lowDiskTestUname, lowDiskTestRoomID,
		"aaa-20200101_000000.flv", []byte("older"))
	newPath := writeRoomMediaFile(t, r.cfg.OutputDir, lowDiskTestUname, lowDiskTestRoomID,
		"zzz-20260101_000000.flv", []byte("newer"))
	syncLowDiskThreshold(t, r.cfg.OutputDir, r.cfg)

	err := r.ensureDiskSpace(log, lowDiskEnsureParams(true))
	if err != nil {
		t.Fatalf("ensureDiskSpace: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected oldest file removed, stat err=%v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("expected newer file kept: %v", err)
	}
}

func TestIntegration_LowDisk_DoesNotDeleteOtherRooms(t *testing.T) {
	lowDiskIntegrationMu.Lock()
	defer lowDiskIntegrationMu.Unlock()

	r := newLowDiskFxRecorder(t)

	targetOld := writeRoomMediaFile(t, r.cfg.OutputDir, lowDiskTestUname, lowDiskTestRoomID,
		"aaa-20200101_000000.flv", []byte("room-a-old"))
	otherRoom := writeRoomMediaFile(t, r.cfg.OutputDir, lowDiskTestUname, lowDiskOtherRoomID,
		"aaa-20200101_000000.flv", []byte("room-b-old"))
	syncLowDiskThreshold(t, r.cfg.OutputDir, r.cfg)

	err := r.ensureDiskSpace(log, lowDiskEnsureParams(true))
	if err != nil {
		t.Fatalf("ensureDiskSpace: %v", err)
	}
	if _, err := os.Stat(targetOld); !os.IsNotExist(err) {
		t.Fatalf("expected target room oldest removed, stat err=%v", err)
	}
	if _, err := os.Stat(otherRoom); err != nil {
		t.Fatalf("expected other room file untouched: %v", err)
	}
}

func TestIntegration_LowDisk_SkipsActiveWritingBasename(t *testing.T) {
	lowDiskIntegrationMu.Lock()
	defer lowDiskIntegrationMu.Unlock()

	r := newLowDiskFxRecorder(t)

	oldPath := writeRoomMediaFile(t, r.cfg.OutputDir, lowDiskTestUname, lowDiskTestRoomID,
		"aaa-20200101_000000.flv", []byte("deletable"))
	activeName := "zzz-20260101_000000.flv"
	activePath := writeRoomMediaFile(t, r.cfg.OutputDir, lowDiskTestUname, lowDiskTestRoomID,
		activeName, []byte("active"))
	r.writingFiles.Add(activeName)
	syncLowDiskThreshold(t, r.cfg.OutputDir, r.cfg)

	err := r.ensureDiskSpace(log, lowDiskEnsureParams(true))
	if err != nil {
		t.Fatalf("ensureDiskSpace: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected deletable oldest removed, stat err=%v", err)
	}
	if _, err := os.Stat(activePath); err != nil {
		t.Fatalf("expected active writing file kept: %v", err)
	}
}

func TestIntegration_LowDisk_RecoverySkipsSessionOutputPath(t *testing.T) {
	lowDiskIntegrationMu.Lock()
	defer lowDiskIntegrationMu.Unlock()

	r := newLowDiskFxRecorder(t)

	olderPath := writeRoomMediaFile(t, r.cfg.OutputDir, lowDiskTestUname, lowDiskTestRoomID,
		"aaa-20200101_000000.flv", []byte("older-history"))
	currentPath := writeRoomMediaFile(t, r.cfg.OutputDir, lowDiskTestUname, lowDiskTestRoomID,
		"zzz-20260101_000000.flv", []byte("current-segment"))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	info := &Info{
		ctx:    ctx,
		cancel: cancel,
		room:   &bilibili.LiveRoomInfoDetail{RoomID: lowDiskTestRoomID, Uname: lowDiskTestUname},
		startOptions: snapshotStartOptions(RecordStartOptions{
			deleteOldestOnLowDisk: true,
		}),
	}
	info.SetOutputPath(currentPath)
	info.status.Store(recoveringPtr)
	r.recording.Store(lowDiskTestRoomID, info)

	syncLowDiskThreshold(t, r.cfg.OutputDir, r.cfg)
	err := r.internalStart(internalStartParams{
		roomId:  lowDiskTestRoomID,
		mode:    startModeRecovery,
		session: info,
		ctx:     ctx,
		opts: RecordStartOptions{
			deleteOldestOnLowDisk: true,
		},
	})
	assertLowDiskStartPassedPurge(t, err)
	if _, err := os.Stat(olderPath); !os.IsNotExist(err) {
		t.Fatalf("expected older history removed, stat err=%v", err)
	}
	if _, err := os.Stat(currentPath); err != nil {
		t.Fatalf("expected recovery session output kept: %v", err)
	}
}

func TestIntegration_LowDisk_RemovesDanmakuSidecarsWithMedia(t *testing.T) {
	lowDiskIntegrationMu.Lock()
	defer lowDiskIntegrationMu.Unlock()

	r := newLowDiskFxRecorder(t)

	videoPath := writeRoomMediaFile(t, r.cfg.OutputDir, lowDiskTestUname, lowDiskTestRoomID,
		"aaa-20200101_000000.flv", []byte("video"))
	jsonlPath := danmaku.PathForVideo(videoPath, ".jsonl")
	if err := os.WriteFile(jsonlPath, []byte("{}"), 0644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	syncLowDiskThreshold(t, r.cfg.OutputDir, r.cfg)

	err := r.ensureDiskSpace(log, lowDiskEnsureParams(true))
	if err != nil {
		t.Fatalf("ensureDiskSpace: %v", err)
	}
	if _, err := os.Stat(videoPath); !os.IsNotExist(err) {
		t.Fatalf("expected video removed, stat err=%v", err)
	}
	if _, err := os.Stat(jsonlPath); !os.IsNotExist(err) {
		t.Fatalf("expected jsonl sidecar removed, stat err=%v", err)
	}
}

func TestIntegration_LowDisk_ExhaustedDeletableReturns507(t *testing.T) {
	lowDiskIntegrationMu.Lock()
	defer lowDiskIntegrationMu.Unlock()

	r := newLowDiskFxRecorder(t)

	onlyName := "zzz-20260101_000000.flv"
	onlyPath := writeRoomMediaFile(t, r.cfg.OutputDir, lowDiskTestUname, lowDiskTestRoomID,
		onlyName, []byte("only-segment"))
	r.writingFiles.Add(onlyName)
	syncLowDiskThreshold(t, r.cfg.OutputDir, r.cfg)

	err := r.ensureDiskSpace(log, lowDiskEnsureParams(true))
	if !errors.Is(err, ErrInsufficientDiskSpace) {
		t.Fatalf("expected ErrInsufficientDiskSpace, got %v", err)
	}
	if _, err := os.Stat(onlyPath); err != nil {
		t.Fatalf("expected protected only file to remain: %v", err)
	}
}

func TestIntegration_LowDisk_SecondReserveBlockedWhileFirstPending(t *testing.T) {
	lowDiskIntegrationMu.Lock()
	defer lowDiskIntegrationMu.Unlock()

	r := newLowDiskFxRecorder(t)

	writeRoomMediaFile(t, r.cfg.OutputDir, lowDiskTestUname, lowDiskTestRoomID,
		"aaa-20200101_000000.flv", []byte("old"))
	// Extra segment so ensureDiskSpace can purge more than once if the runner's
	// global free space dips while other packages run go test ./... in parallel.
	writeRoomMediaFile(t, r.cfg.OutputDir, lowDiskTestUname, lowDiskTestRoomID,
		"zzz-20260101_000000.flv", []byte("buffer"))

	txn := r.reser.Begin()
	if err := txn.Reserve(lowDiskTestRoomID); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	defer txn.Abort(lowDiskTestRoomID)

	txn2 := r.reser.Begin()
	if err := txn2.Reserve(lowDiskTestRoomID); !errors.Is(err, tx.ErrAlreadyReserved) {
		t.Fatalf("expected ErrAlreadyReserved for concurrent start, got %v", err)
	}

	err := r.Start(lowDiskTestRoomID, WithDeleteOldestOnLowDisk(true))
	if !errors.Is(err, ErrRecordingPending) {
		t.Fatalf("expected ErrRecordingPending while reservation held, got %v", err)
	}

	syncLowDiskThreshold(t, r.cfg.OutputDir, r.cfg)
	err = r.ensureDiskSpace(log, lowDiskEnsureParams(true))
	if err != nil {
		t.Fatalf("ensureDiskSpace under reservation: %v", err)
	}
}

func newLowDiskFxRecorder(t *testing.T) *Service {
	t.Helper()
	t.Setenv("OUTPUT_DIR", t.TempDir())
	t.Setenv("DATABASE_DIR", t.TempDir())
	t.Setenv("BILIBILI_LOGIN_MODE", "anonymous")

	var r *Service
	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		metrics.Module,
		fx.Provide(path.NewService),
		fx.Provide(stream.NewService),
		fx.Provide(room.NewService),
		fx.Provide(webhook.NewService),
		fx.Provide(convert.NewService),
		fx.Provide(notify.NewService),
		fx.Provide(danmaku.NewService),
		fx.Provide(NewService),
		fx.Populate(&r),
		fx.StartTimeout(60*time.Second),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	return r
}

func assertLowDiskStartPassedPurge(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, ErrInsufficientDiskSpace) {
		t.Fatalf("disk purge did not free enough space: %v", err)
	}
	// Room 88001 is not a guaranteed live test room; any post-disk error is fine.
	if err == nil {
		t.Fatal("unexpected successful start without live stream fixture")
	}
}

func lowDiskEnsureParams(deleteOldest bool) internalStartParams {
	return internalStartParams{
		roomId: lowDiskTestRoomID,
		opts: RecordStartOptions{
			deleteOldestOnLowDisk: deleteOldest,
		},
	}
}

// syncLowDiskThreshold sets MinDiskSpaceBytes so the check is barely failing until
// purge frees space. Uses mount-wide Free; call immediately before ensureDiskSpace
// (or internalStart). Pair with lowDiskTestMediaSize large enough to absorb CI
// runners where other packages shift Free while go test ./... runs in parallel.
func syncLowDiskThreshold(t *testing.T, outputDir string, cfg *config.Config) {
	t.Helper()
	usage, err := utils.GetDiskSpace(outputDir)
	if err != nil {
		t.Fatalf("GetDiskSpace: %v", err)
	}
	cfg.MinDiskSpaceBytes = int64(usage.Free) + 1
}

func writeRoomMediaFile(t *testing.T, outputDir, uname string, roomID int, baseName string, content []byte) string {
	t.Helper()
	dir := filepath.Join(outputDir, uname+"-"+strconv.Itoa(roomID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, baseName)
	payload := lowDiskTestFilePayload(content)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func lowDiskTestFilePayload(seed []byte) []byte {
	if len(seed) >= lowDiskTestMediaSize {
		return seed
	}
	out := make([]byte, lowDiskTestMediaSize)
	copy(out, seed)
	return out
}
