package convert_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/convert"
	"github.com/bilirec/bilirec/internal/services/path"
	"github.com/bilirec/bilirec/pkg/ffmpeg"
	"github.com/bilirec/bilirec/utils"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestFFmpegFailureCooldownAllowsOtherTaskIntegration(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("ffmpeg not available in PATH, skipping integration test")
	}

	dir := t.TempDir()
	t.Setenv("DATABASE_DIR", filepath.Join(dir, "database"))
	t.Setenv("OUTPUT_DIR", dir)
	t.Setenv("SECRET_DIR", filepath.Join(dir, "secrets"))
	t.Setenv("FFMPEG_CHECK_INTERVAL_SECS", "1")
	t.Setenv("FFMPEG_MAX_CONCURRENT_TASKS", "1")
	t.Setenv("CLOUDCONVERT_API_KEY", "")

	var svc *convert.Service
	app := fxtest.New(t,
		config.Module,
		fx.Provide(path.NewService),
		fx.Provide(convert.NewService),
		fx.Populate(&svc),
	)
	app.RequireStart()
	defer app.RequireStop()

	badInput := filepath.Join(dir, "bad.flv")
	goodInput := filepath.Join(dir, "good.flv")
	goodOutput := utils.ChangePathFormat(goodInput, "mp4")

	if err := os.WriteFile(badInput, []byte("not-a-valid-flv"), 0644); err != nil {
		t.Fatal(err)
	}
	createMinimalFLV(t, goodInput)

	badQueue, err := svc.Enqueue(badInput, "mp4", false)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for the first poll + failed convert + cooldown window to begin.
	time.Sleep(2500 * time.Millisecond)

	goodQueue, err := svc.Enqueue(goodInput, "mp4", false)
	if err != nil {
		t.Fatal(err)
	}

	waitUntil(t, 20*time.Second, func() bool {
		if _, err := os.Stat(goodOutput); err != nil {
			return false
		}
		queues, err := svc.ListInProgress()
		if err != nil {
			t.Fatalf("list in-progress: %v", err)
		}
		for _, q := range queues {
			if q.TaskID == goodQueue.TaskID {
				return false
			}
		}
		return true
	})

	queues, err := svc.ListInProgress()
	if err != nil {
		t.Fatal(err)
	}
	if len(queues) != 1 {
		t.Fatalf("expected exactly one remaining task, got %d", len(queues))
	}
	if queues[0].TaskID != badQueue.TaskID {
		t.Fatalf("expected remaining task %s, got %s", badQueue.TaskID, queues[0].TaskID)
	}
}

func createMinimalFLV(t *testing.T, outPath string) {
	t.Helper()
	cmd := exec.Command("ffmpeg",
		"-y",
		"-hide_banner",
		"-loglevel", "error",
		"-f", "lavfi",
		"-i", "color=c=black:s=320x240:r=5",
		"-t", "0.2",
		"-c:v", "libx264",
		"-an",
		"-f", "flv",
		outPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create minimal flv %s: %v\n%s", outPath, err, out)
	}
}

func waitUntil(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}
