package convert

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/internal/services/path"
	"github.com/bilirec/bilirec/pkg/ffmpeg"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

const (
	seriesFFmpegPending    = `bilirec_convert_tasks_pending{provider="ffmpeg"}`
	seriesFFmpegProcessing = `bilirec_convert_tasks_processing{provider="ffmpeg"}`
	seriesFFmpegQueued     = `bilirec_convert_tasks_queued_total{provider="ffmpeg"}`
	seriesFFmpegCompleted  = `bilirec_convert_tasks_completed_total{provider="ffmpeg"}`
	seriesFFmpegFailed     = `bilirec_convert_tasks_failed_total{provider="ffmpeg"}`
	seriesFFmpegCancelled  = `bilirec_convert_tasks_cancelled_total{provider="ffmpeg"}`
)

// startFFmpegConvertApp boots the convert service with metrics enabled and
// only the ffmpeg manager (no CloudConvert API key). Passing intervalSecs "0"
// keeps the default 60s check interval so the worker stays idle during
// synchronous gauge assertions.
func startFFmpegConvertApp(t *testing.T, intervalSecs string) (*Service, *metrics.Exporter) {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("DATABASE_DIR", filepath.Join(dir, "database"))
	t.Setenv("OUTPUT_DIR", dir)
	t.Setenv("SECRET_DIR", filepath.Join(dir, "secrets"))
	t.Setenv("CONVERT_TO_MP4", "true")
	t.Setenv("CLOUDCONVERT_API_KEY", "")
	t.Setenv("METRICS_ENABLED", "true")
	t.Setenv("METRICS_PORT", "0")
	t.Setenv("FFMPEG_CHECK_INTERVAL_SECS", intervalSecs)

	var svc *Service
	var exporter *metrics.Exporter
	app := fxtest.New(t,
		config.Module,
		metrics.Module,
		fx.Provide(path.NewService),
		fx.Provide(NewService),
		fx.Populate(&svc, &exporter),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	return svc, exporter
}

// scrapeValue extracts the sample value of a series from a Prometheus text
// scrape. ok is false when the series has never been set.
func scrapeValue(out, series string) (float64, bool) {
	prefix := series + " "
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			fields := strings.Fields(line)
			v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
			if err != nil {
				return 0, false
			}
			return v, true
		}
	}
	return 0, false
}

func mustScrapeValue(t *testing.T, out, series string) float64 {
	t.Helper()
	v, ok := scrapeValue(out, series)
	if !ok {
		t.Fatalf("series %q not found in scrape:\n%s", series, out)
	}
	return v
}

func waitGauge(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("timed out waiting for gauge condition")
}

func mustCreateFLV(t *testing.T, outPath string) {
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

// mustCreateLargeFLV produces a remux source whose copied output exceeds
// MinimumExportedFileBytesRequired, so ValidateOutputFileSize accepts it and
// asyncProcessTask takes the success path.
func mustCreateLargeFLV(t *testing.T, outPath string) {
	t.Helper()
	cmd := exec.Command("ffmpeg",
		"-y",
		"-hide_banner",
		"-loglevel", "error",
		"-f", "lavfi",
		"-i", "testsrc=size=640x360:rate=30:duration=20",
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-crf", "0",
		"-an",
		"-f", "flv",
		outPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create large flv %s: %v\n%s", outPath, err, out)
	}
	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() < MinimumExportedFileBytesRequired*2 {
		t.Fatalf("generated flv is %dB, want >= %dB for the success-path test", info.Size(), MinimumExportedFileBytesRequired*2)
	}
}

// TestFFmpegEnqueueUpdatesGaugeImmediately verifies that the pending gauge is
// refreshed by the enqueue event itself, without waiting for the check ticker.
func TestFFmpegEnqueueUpdatesGaugeImmediately(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("ffmpeg not available in PATH, skipping integration test")
	}

	svc, exporter := startFFmpegConvertApp(t, "0")

	input := filepath.Join(t.TempDir(), "a.flv")
	mustCreateFLV(t, input)

	if _, err := svc.Enqueue(input, "mp4", false); err != nil {
		t.Fatal(err)
	}

	out := exporter.Scrape()
	if got := mustScrapeValue(t, out, seriesFFmpegPending); got != 1 {
		t.Fatalf("pending gauge = %v, want 1 immediately after enqueue", got)
	}
	if got := mustScrapeValue(t, out, seriesFFmpegProcessing); got != 0 {
		t.Fatalf("processing gauge = %v, want 0 after enqueue", got)
	}
	if got := mustScrapeValue(t, out, seriesFFmpegQueued); got != 1 {
		t.Fatalf("queued counter = %v, want 1 after enqueue", got)
	}
}

// TestFFmpegCancelUpdatesGaugeImmediately verifies that Cancel refreshes the
// pending gauge and bumps the cancelled counter synchronously.
func TestFFmpegCancelUpdatesGaugeImmediately(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("ffmpeg not available in PATH, skipping integration test")
	}

	svc, exporter := startFFmpegConvertApp(t, "0")

	input := filepath.Join(t.TempDir(), "b.flv")
	mustCreateFLV(t, input)

	q, err := svc.Enqueue(input, "mp4", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(q.TaskID); err != nil {
		t.Fatal(err)
	}

	out := exporter.Scrape()
	if got := mustScrapeValue(t, out, seriesFFmpegPending); got != 0 {
		t.Fatalf("pending gauge = %v, want 0 immediately after cancel", got)
	}
	if got := mustScrapeValue(t, out, seriesFFmpegCancelled); got != 1 {
		t.Fatalf("cancelled counter = %v, want 1 after cancel", got)
	}
}

// submitDirectly replicates the worker-loop submission for one task so the
// processing gauge can be asserted deterministically before completion.
func submitDirectly(t *testing.T, mgr *ffmpegConvertManager, q *TaskQueue) context.CancelFunc {
	t.Helper()
	if !mgr.concurrent.TryAcquire(1) {
		t.Fatal("failed to acquire ffmpeg concurrency slot")
	}
	_, cancel := context.WithCancel(context.Background())
	mgr.processing.Store(q.TaskID, cancel)
	mgr.updateGaugeMetrics()
	return cancel
}

// TestFFmpegGaugeLifecycleSuccess drives the same submission/finish code
// paths as the worker loop synchronously: pending -> processing -> 0, with
// the completed counter bumped exactly once.
func TestFFmpegGaugeLifecycleSuccess(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("ffmpeg not available in PATH, skipping integration test")
	}

	svc, exporter := startFFmpegConvertApp(t, "0")

	input := filepath.Join(t.TempDir(), "c.flv")
	mustCreateLargeFLV(t, input)

	q, err := svc.Enqueue(input, "mp4", false)
	if err != nil {
		t.Fatal(err)
	}

	mgr, ok := svc.managers["ffmpeg"].(*ffmpegConvertManager)
	if !ok {
		t.Fatal("ffmpeg manager not registered")
	}

	cancel := submitDirectly(t, mgr, q)
	defer cancel()

	out := exporter.Scrape()
	if got := mustScrapeValue(t, out, seriesFFmpegProcessing); got != 1 {
		t.Fatalf("processing gauge = %v, want 1 while task submitted", got)
	}
	if got := mustScrapeValue(t, out, seriesFFmpegPending); got != 0 {
		t.Fatalf("pending gauge = %v, want 0 while task processing", got)
	}

	mgr.asyncProcessTask(context.Background(), q, log)

	out = exporter.Scrape()
	if got := mustScrapeValue(t, out, seriesFFmpegProcessing); got != 0 {
		t.Fatalf("processing gauge = %v, want 0 after completion", got)
	}
	if got := mustScrapeValue(t, out, seriesFFmpegPending); got != 0 {
		t.Fatalf("pending gauge = %v, want 0 after completion", got)
	}
	if got := mustScrapeValue(t, out, seriesFFmpegCompleted); got != 1 {
		t.Fatalf("completed counter = %v, want 1 after completion", got)
	}
}

// TestFFmpegGaugeLifecycleFailure covers the failed-convert path: the task
// leaves processing, returns to pending (retry with cooldown), and the failed
// counter is bumped.
func TestFFmpegGaugeLifecycleFailure(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("ffmpeg not available in PATH, skipping integration test")
	}

	svc, exporter := startFFmpegConvertApp(t, "0")

	input := filepath.Join(t.TempDir(), "garbage.flv")
	if err := os.WriteFile(input, []byte("not-a-valid-flv"), 0644); err != nil {
		t.Fatal(err)
	}

	q, err := svc.Enqueue(input, "mp4", false)
	if err != nil {
		t.Fatal(err)
	}

	mgr, ok := svc.managers["ffmpeg"].(*ffmpegConvertManager)
	if !ok {
		t.Fatal("ffmpeg manager not registered")
	}

	cancel := submitDirectly(t, mgr, q)
	defer cancel()

	mgr.asyncProcessTask(context.Background(), q, log)

	out := exporter.Scrape()
	if got := mustScrapeValue(t, out, seriesFFmpegProcessing); got != 0 {
		t.Fatalf("processing gauge = %v, want 0 after failure", got)
	}
	if got := mustScrapeValue(t, out, seriesFFmpegPending); got != 1 {
		t.Fatalf("pending gauge = %v, want 1 after failure (task returned to queue)", got)
	}
	if got := mustScrapeValue(t, out, seriesFFmpegFailed); got != 1 {
		t.Fatalf("failed counter = %v, want 1 after failure", got)
	}
}

// TestFFmpegMissingInputCountsAsCancelled runs the real worker loop against a
// queue entry whose input file vanished: the task must be removed from the
// queue, counted as cancelled, and reflected in the pending gauge.
func TestFFmpegMissingInputCountsAsCancelled(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("ffmpeg not available in PATH, skipping integration test")
	}

	svc, exporter := startFFmpegConvertApp(t, "5")

	input := filepath.Join(t.TempDir(), "missing.flv")
	mustCreateFLV(t, input)

	if _, err := svc.Enqueue(input, "mp4", false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(input); err != nil {
		t.Fatal(err)
	}

	waitGauge(t, 20*time.Second, func() bool {
		out := exporter.Scrape()
		cancelled, cancelledOK := scrapeValue(out, seriesFFmpegCancelled)
		pending, pendingOK := scrapeValue(out, seriesFFmpegPending)
		return cancelledOK && cancelled == 1 && pendingOK && pending == 0
	})
}
