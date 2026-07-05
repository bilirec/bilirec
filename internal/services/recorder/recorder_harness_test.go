package recorder_test

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof on http.DefaultServeMux
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/convert"
	"github.com/bilirec/bilirec/internal/services/notify"
	"github.com/bilirec/bilirec/internal/services/path"
	"github.com/bilirec/bilirec/internal/services/recorder"
	"github.com/bilirec/bilirec/internal/services/room"
	"github.com/bilirec/bilirec/internal/services/stream"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/process"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

const (
	recorderTestProfileDirEnv        = "RECORDER_TEST_PROFILE_DIR"
	recorderCPUSteadySampleEnv       = "RECORDER_CPU_STEADY_SAMPLE_SECS"
	recorderCPUSteadySampleMin       = 1 * time.Second
	recorderTestSettleAfterStop      = 5 * time.Second
	pprofLogTopN                     = 10
	recorderRecordProfileIntervalEnv      = "RECORDER_RECORD_PROFILE_INTERVAL_SECS"
	recorderProfileLogTopEnv              = "RECORDER_PROFILE_LOG_TOP" // default false: save only during test
	recorderTestMaxRetainedAllocMBEnv     = "RECORDER_TEST_MAX_RETAINED_ALLOC_MB"
	recorderTestMaxRetainedSysMBEnv       = "RECORDER_TEST_MAX_RETAINED_SYS_MB"
	recorderTestMaxRetainedAllocPerRoomEnv = "RECORDER_TEST_MAX_RETAINED_ALLOC_MB_PER_ROOM"
	recorderTestMaxRetainedSysPerRoomEnv   = "RECORDER_TEST_MAX_RETAINED_SYS_MB_PER_ROOM"
)

// recorderTestSession wires fx app lifecycle with cross-platform profiling:
//   - local debug/pprof HTTP (heap / goroutine / profile / trace)
//   - gopsutil CPU phase metrics (util% without parsing pprof during test)
//   - save-only heap/goroutine snapshots on an interval during recording
//   - one continuous CPU profile (scheme B) per recording window; analyze at end
type recorderTestSession struct {
	t        *testing.T
	app      *fxtest.App
	Recorder *recorder.Service
	Room     *room.Service
	Monitor  *recorderTestMonitor
}

func newRecorderTestSession(t *testing.T) *recorderTestSession {
	t.Helper()

	monitor := newRecorderTestMonitor(t)
	var recorderService *recorder.Service
	var roomService *room.Service

	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Provide(path.NewService),
		fx.Provide(stream.NewService),
		fx.Provide(room.NewService),
		fx.Provide(convert.NewService),
		fx.Provide(notify.NewService),
		fx.Provide(recorder.NewService),
		fx.Populate(&recorderService, &roomService),
	)
	app.RequireStart()

	sess := &recorderTestSession{
		t:        t,
		app:      app,
		Recorder: recorderService,
		Room:     roomService,
		Monitor:  monitor,
	}
	t.Cleanup(sess.close)
	return sess
}

func (s *recorderTestSession) close() {
	if s.app != nil {
		s.app.RequireStop()
		s.app = nil
	}
	if s.Monitor != nil {
		s.Monitor.Close()
	}
}

// recorderTestMonitor provides cross-platform observability for integration tests.
type recorderTestMonitor struct {
	proc       *process.Process
	profileDir string
	pprofBase  string
	closePprof func()
}

func newRecorderTestMonitor(t *testing.T) *recorderTestMonitor {
	t.Helper()

	profileDir := recorderTestProfileDir(t)
	proc, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		t.Fatalf("process.NewProcess: %v", err)
	}

	pprofBase, closePprof := startTestPprofServer(t)
	m := &recorderTestMonitor{
		proc:       proc,
		profileDir: profileDir,
		pprofBase:  pprofBase,
		closePprof: closePprof,
	}
	m.logEndpoints(t)
	return m
}

func (m *recorderTestMonitor) Close() {
	if m.closePprof != nil {
		m.closePprof()
		m.closePprof = nil
	}
}

func (m *recorderTestMonitor) logEndpoints(t *testing.T) {
	t.Helper()
	t.Logf("recorder test monitor: profile_dir=%s pprof=%s/debug/pprof/ num_cpu=%d num_goroutine=%d",
		m.profileDir, m.pprofBase, runtime.NumCPU(), runtime.NumGoroutine())
	t.Logf("  cpu profile:  %s/debug/pprof/profile?seconds=5", m.pprofBase)
	t.Logf("  heap:         %s/debug/pprof/heap", m.pprofBase)
	t.Logf("  goroutine:    %s/debug/pprof/goroutine", m.pprofBase)
	t.Logf("  trace:        %s/debug/pprof/trace?seconds=5", m.pprofBase)
	t.Logf("  on-disk:      %s/<label>.pprof | <label>_heap.prof | <label>_goroutine.prof", m.profileDir)
	t.Logf("  post-run:     logAnalysisHints() dumps pprof top %d for saved profiles", pprofLogTopN)
}

func (m *recorderTestMonitor) logAnalysisHints(t *testing.T) {
	t.Helper()
	t.Logf("--- saved profile analysis (post-run, profile_dir=%s) ---", m.profileDir)
	m.flushSavedCPUProfiles(t)
	m.flushSavedHeapProfiles(t)
	m.flushSavedGoroutineProfiles(t)
	t.Logf("interactive: go tool pprof -http=:8080 %s/<file>", m.profileDir)
	t.Logf("cpu time ranges: go tool pprof -http=:8080 %s/<recording>.pprof  (use Sample menu)", m.profileDir)
}

type memorySnapshot struct {
	Label        string
	AllocMB      float64
	HeapInuseMB  float64
	StackInuseMB float64
	SysMB        float64
	HeapPath     string
	Goroutines   int
}

func (m *recorderTestMonitor) snapshotMemory(t *testing.T, label string, runGC bool) memorySnapshot {
	t.Helper()
	if runGC {
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	heapPath := m.writeHeapProfile(label)
	snap := memorySnapshot{
		Label:        label,
		AllocMB:      float64(ms.Alloc) / (1024 * 1024),
		HeapInuseMB:  float64(ms.HeapInuse) / (1024 * 1024),
		StackInuseMB: float64(ms.StackInuse) / (1024 * 1024),
		SysMB:        float64(ms.Sys) / (1024 * 1024),
		HeapPath:     heapPath,
		Goroutines:   runtime.NumGoroutine(),
	}
	t.Logf("mem %s: alloc=%.2f MB heap_inuse=%.2f MB stack_inuse=%.2f MB sys=%.2f MB goroutines=%d heap=%s",
		label, snap.AllocMB, snap.HeapInuseMB, snap.StackInuseMB, snap.SysMB, snap.Goroutines, heapPath)
	maybeLogHeapPprofTop(t, label, heapPath)
	return snap
}

func (m *recorderTestMonitor) snapshotGoroutines(t *testing.T, label string) int {
	t.Helper()
	n := runtime.NumGoroutine()
	goroutinePath := m.fetchPprofSnapshot(t, label, "_goroutine.prof", "/debug/pprof/goroutine")
	t.Logf("goroutines %s: count=%d profile=%s", label, n, goroutinePath)
	maybeLogGoroutinePprofTop(t, label, goroutinePath)
	return n
}

func (m *recorderTestMonitor) fetchPprofSnapshot(t *testing.T, label, suffix, endpoint string) string {
	t.Helper()
	resp, err := http.Get(m.pprofBase + endpoint)
	if err != nil {
		t.Logf("fetch %s for %s failed: %v", endpoint, label, err)
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Logf("fetch %s for %s: status %d", endpoint, label, resp.StatusCode)
		return ""
	}

	path := filepath.Join(m.profileDir, sanitizeProfileLabel(label)+suffix)
	f, err := os.Create(path)
	if err != nil {
		t.Logf("create goroutine profile %s: %v", path, err)
		return ""
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		t.Logf("write profile %s: %v", path, err)
		return ""
	}
	_ = f.Close()
	return path
}

func (m *recorderTestMonitor) writeHeapProfile(label string) string {
	safe := sanitizeProfileLabel(label)
	path := filepath.Join(m.profileDir, safe+"_heap.prof")
	f, err := os.Create(path)
	if err != nil {
		return ""
	}
	if err := pprof.WriteHeapProfile(f); err != nil {
		_ = f.Close()
		return ""
	}
	_ = f.Close()
	return path
}

func sanitizeProfileLabel(label string) string {
	out := make([]byte, 0, len(label))
	for i := 0; i < len(label); i++ {
		c := label[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "snapshot"
	}
	return string(out)
}

type cpuPhaseReport struct {
	Name          string
	Wall          time.Duration
	CPUTime       time.Duration
	UtilPercent   float64
	ProfilePath   string
	AvgCPUPercent float64
}

func (m *recorderTestMonitor) beginPhase(name string) (*cpuPhase, error) {
	times, err := m.proc.Times()
	if err != nil {
		return nil, err
	}

	profilePath := filepath.Join(m.profileDir, sanitizeProfileLabel(name)+".pprof")
	f, err := os.Create(profilePath)
	if err != nil {
		return nil, err
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		f.Close()
		return nil, err
	}

	return &cpuPhase{
		monitor:     m,
		name:        name,
		wallStart:   time.Now(),
		timesStart:  times,
		profilePath: profilePath,
		profileFile: f,
	}, nil
}

type cpuPhase struct {
	monitor     *recorderTestMonitor
	name        string
	wallStart   time.Time
	timesStart  *cpu.TimesStat
	profilePath string
	profileFile *os.File
}

func (p *cpuPhase) end(t *testing.T) cpuPhaseReport {
	t.Helper()

	pprof.StopCPUProfile()
	_ = p.profileFile.Close()
	maybeLogCPUPprofTop(t, p.name, p.profilePath)

	wall := time.Since(p.wallStart)
	report := cpuPhaseReport{
		Name:        p.name,
		Wall:        wall,
		ProfilePath: p.profilePath,
	}

	timesEnd, err := p.monitor.proc.Times()
	if err != nil {
		t.Logf("phase %s: process.Times after window failed: %v", p.name, err)
		return report
	}

	cpuSeconds := timesEnd.Total() - p.timesStart.Total()
	report.CPUTime = time.Duration(cpuSeconds * float64(time.Second))
	report.UtilPercent = cpuUtilizationPercent(report.CPUTime, wall)
	return report
}

func (m *recorderTestMonitor) measureAvgCPU(t *testing.T, interval time.Duration) float64 {
	t.Helper()
	pct, err := m.proc.Percent(interval)
	if err != nil {
		t.Logf("process.Percent(%s) failed: %v", interval, err)
		return 0
	}
	return pct
}

func logCPUPhase(t *testing.T, report cpuPhaseReport) {
	t.Helper()
	switch {
	case report.AvgCPUPercent > 0 && report.UtilPercent > 0:
		t.Logf("cpu phase %s: wall=%s cpu_time=%s util=%.1f%% avg_cpu=%.1f%% of %d cores profile=%s",
			report.Name,
			report.Wall.Round(time.Microsecond),
			report.CPUTime.Round(time.Microsecond),
			report.UtilPercent,
			report.AvgCPUPercent,
			runtime.NumCPU(),
			report.ProfilePath,
		)
	case report.AvgCPUPercent > 0:
		t.Logf("cpu phase %s: wall=%s avg_cpu=%.1f%% profile=%s",
			report.Name, report.Wall.Round(time.Microsecond), report.AvgCPUPercent, report.ProfilePath)
	default:
		t.Logf("cpu phase %s: wall=%s cpu_time=%s util=%.1f%% of %d cores profile=%s",
			report.Name,
			report.Wall.Round(time.Microsecond),
			report.CPUTime.Round(time.Microsecond),
			report.UtilPercent,
			runtime.NumCPU(),
			report.ProfilePath,
		)
	}
}

func logMemoryDelta(t *testing.T, before, after memorySnapshot) {
	t.Helper()
	t.Logf("mem delta %s -> %s: alloc %+.2f MB goroutines %+d",
		before.Label, after.Label,
		after.AllocMB-before.AllocMB,
		after.Goroutines-before.Goroutines,
	)
}

func handleRecordingStartErr(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	switch err {
	case recorder.ErrStreamNotLive:
		t.Skip("stream not live")
	case recorder.ErrEmptyStreamURLs:
		t.Skip("no stream URLs available")
	case recorder.ErrStreamURLsUnreachable:
		t.Skip("stream URLs unreachable")
	case recorder.ErrRecordRecovering:
		t.Skip("room recovering")
	case recorder.ErrRecordingPending:
		t.Skip("recording start pending")
	default:
		t.Fatal(err)
	}
}

func recorderTestProfileDir(t *testing.T) string {
	t.Helper()
	if base := os.Getenv(recorderTestProfileDirEnv); base != "" {
		dir := filepath.Join(base, sanitizeProfileLabel(t.Name()))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create profile dir %q: %v", dir, err)
		}
		return dir
	}
	return t.TempDir()
}

func recorderCPUSteadySampleDuration() time.Duration {
	raw := os.Getenv(recorderCPUSteadySampleEnv)
	if raw == "" {
		return recorderCPUSteadySampleMin
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	if secs, err := time.ParseDuration(raw + "s"); err == nil && secs > 0 {
		return secs
	}
	return recorderCPUSteadySampleMin
}

func integrationRecordDuration() time.Duration {
	if os.Getenv("CI") != "" {
		return 15 * time.Minute
	}
	return 3 * time.Minute
}

// recordingSnapshotInterval controls how often heap/goroutine profiles are saved
// during a recording window (CPU uses one continuous profile for the whole window).
func recordingSnapshotInterval(total time.Duration) time.Duration {
	if raw := os.Getenv(recorderRecordProfileIntervalEnv); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
		if secs, err := time.ParseDuration(raw + "s"); err == nil && secs > 0 {
			return secs
		}
	}
	if total <= 90*time.Second {
		return total
	}
	if os.Getenv("CI") != "" {
		return 3 * time.Minute
	}
	return 1 * time.Minute
}

// runRecordingProfiledWait holds one continuous CPU profile (scheme B) for the full
// duration while saving heap/goroutine snapshots at recordingSnapshotInterval.
// pprof top output is deferred to logAnalysisHints unless RECORDER_PROFILE_LOG_TOP=true.
func (m *recorderTestMonitor) runRecordingProfiledWait(t *testing.T, label string, duration time.Duration) cpuPhaseReport {
	t.Helper()

	interval := recordingSnapshotInterval(duration)
	if interval <= 0 || interval > duration {
		interval = duration
	}

	t.Logf("recording save-only wait: label=%s total=%s snapshot_interval=%s snapshots≈%d cpu=continuous",
		label, duration.Round(time.Second), interval.Round(time.Second), int(duration/interval)+1)

	phase, err := m.beginPhase(label)
	if err != nil {
		t.Fatalf("begin recording cpu phase %s: %v", label, err)
	}

	elapsed := time.Duration(0)
	tick := 0
	for elapsed < duration {
		chunkDur := interval
		if remaining := duration - elapsed; remaining < chunkDur {
			chunkDur = remaining
		}
		time.Sleep(chunkDur)
		elapsed += chunkDur
		tick++

		snapLabel := fmt.Sprintf("%s_tick%03d_elapsed_%s", label, tick, elapsed.Round(time.Second))
		m.snapshotMemory(t, snapLabel, false)
		m.snapshotGoroutines(t, snapLabel)
	}

	report := phase.end(t)
	logCPUPhase(t, report)
	return report
}

func startTestPprofServer(t *testing.T) (baseURL string, stop func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen pprof: %v", err)
	}

	srv := &http.Server{Handler: http.DefaultServeMux}
	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			t.Logf("pprof server stopped: %v", serveErr)
		}
	}()

	return "http://" + ln.Addr().String(), func() { _ = srv.Close() }
}

func shouldLogPprofTopImmediately() bool {
	return os.Getenv(recorderProfileLogTopEnv) == "true"
}

func maybeLogCPUPprofTop(t *testing.T, label, path string) {
	if shouldLogPprofTopImmediately() {
		logPprofTopN(t, "cpu", label, path)
	}
}

func maybeLogHeapPprofTop(t *testing.T, label, heapPath string) {
	if !shouldLogPprofTopImmediately() || heapPath == "" {
		return
	}
	logPprofTopNIndexed(t, "heap", label+"_inuse", heapPath, "inuse_space")
	logPprofTopNIndexed(t, "heap", label+"_alloc", heapPath, "alloc_space")
}

func maybeLogGoroutinePprofTop(t *testing.T, label, path string) {
	if shouldLogPprofTopImmediately() && path != "" {
		logPprofTopN(t, "goroutine", label, path)
	}
}

func (m *recorderTestMonitor) flushSavedCPUProfiles(t *testing.T) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(m.profileDir, "*.pprof"))
	if err != nil {
		t.Logf("glob cpu profiles: %v", err)
		return
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		t.Log("no saved cpu profiles (*.pprof)")
		return
	}
	t.Logf("saved cpu profiles: %d", len(paths))
	for _, p := range paths {
		logPprofTopN(t, "cpu", filepath.Base(p), p)
	}
}

func (m *recorderTestMonitor) flushSavedHeapProfiles(t *testing.T) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(m.profileDir, "*_heap.prof"))
	if err != nil {
		t.Logf("glob heap profiles: %v", err)
		return
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		t.Log("no saved heap profiles (*_heap.prof)")
		return
	}
	t.Logf("saved heap profiles: %d", len(paths))
	for _, p := range paths {
		base := strings.TrimSuffix(filepath.Base(p), "_heap.prof")
		logPprofTopNIndexed(t, "heap", base+"_inuse", p, "inuse_space")
		logPprofTopNIndexed(t, "heap", base+"_alloc", p, "alloc_space")
	}
}

func (m *recorderTestMonitor) flushSavedGoroutineProfiles(t *testing.T) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(m.profileDir, "*_goroutine.prof"))
	if err != nil {
		t.Logf("glob goroutine profiles: %v", err)
		return
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		t.Log("no saved goroutine profiles (*_goroutine.prof)")
		return
	}
	t.Logf("saved goroutine profiles: %d", len(paths))
	for _, p := range paths {
		logPprofTopN(t, "goroutine", filepath.Base(p), p)
	}
}

func logPprofTopN(t *testing.T, category, label, source string) {
	logPprofTopNIndexed(t, category, label, source, "")
}

func logPprofTopNIndexed(t *testing.T, category, label, source, sampleIndex string) {
	t.Helper()
	if source == "" {
		return
	}
	args := []string{
		"tool", "pprof",
		"-nodecount=" + strconv.Itoa(pprofLogTopN),
		"-top",
	}
	if sampleIndex != "" {
		args = append(args, "-sample_index="+sampleIndex)
	}
	args = append(args, source)

	out, err := exec.Command("go", args...).CombinedOutput()
	if err != nil {
		t.Logf("pprof top %d %s (%s) source=%s failed: %v", pprofLogTopN, category, label, source, err)
		if len(out) > 0 {
			t.Logf("pprof output:\n%s", string(out))
		}
		return
	}
	t.Logf("pprof top %d %s (%s) source=%s:\n%s", pprofLogTopN, category, label, source, string(out))
}

func cpuUtilizationPercent(cpuTime, wallTime time.Duration) float64 {
	if wallTime <= 0 {
		return 0
	}
	cores := runtime.NumCPU()
	if cores <= 0 {
		cores = 1
	}
	return float64(cpuTime) / float64(wallTime) / float64(cores) * 100
}

func memAllocDiffMB(after, before memorySnapshot) float64 {
	return after.AllocMB - before.AllocMB
}

func memSysDiffMB(after, before memorySnapshot) float64 {
	return after.SysMB - before.SysMB
}

// recordingMemoryBudget caps heap and runtime Sys growth retained after a full
// record/stop/cleanup cycle relative to the pre-record baseline.
type recordingMemoryBudget struct {
	label              string
	maxRetainedAllocMB float64
	maxRetainedSysMB   float64
}

func recordingMemoryBudgetForSessions(concurrentSessions int, label string) recordingMemoryBudget {
	const (
		baseAllocMB     = 18.0
		perSessionAlloc = 12.0
		baseSysMB       = 50.0
		perSessionSys   = 20.0
	)

	budget := recordingMemoryBudget{
		label:              label,
		maxRetainedAllocMB: baseAllocMB + float64(concurrentSessions)*perSessionAlloc,
		maxRetainedSysMB:   baseSysMB + float64(concurrentSessions)*perSessionSys,
	}

	if v := strings.TrimSpace(os.Getenv(recorderTestMaxRetainedAllocMBEnv)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			budget.maxRetainedAllocMB = f
		}
	}
	if v := strings.TrimSpace(os.Getenv(recorderTestMaxRetainedSysMBEnv)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			budget.maxRetainedSysMB = f
		}
	}
	if v := strings.TrimSpace(os.Getenv(recorderTestMaxRetainedAllocPerRoomEnv)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			budget.maxRetainedAllocMB = baseAllocMB + float64(concurrentSessions)*f
		}
	}
	if v := strings.TrimSpace(os.Getenv(recorderTestMaxRetainedSysPerRoomEnv)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			budget.maxRetainedSysMB = baseSysMB + float64(concurrentSessions)*f
		}
	}
	return budget
}

// snapshotMemoryReleased mirrors production idle cleanup: GC plus returning unused
// pages to the OS (janitor uses the same FreeOSMemory path).
func (m *recorderTestMonitor) snapshotMemoryReleased(t *testing.T, label string) memorySnapshot {
	t.Helper()
	runtime.GC()
	runtime.GC()
	debug.FreeOSMemory()
	time.Sleep(100 * time.Millisecond)
	return m.snapshotMemory(t, label, false)
}

func assertRecordingMemoryReleased(t *testing.T, baseline, after memorySnapshot, budget recordingMemoryBudget) {
	t.Helper()

	retainedAlloc := memAllocDiffMB(after, baseline)
	retainedSys := memSysDiffMB(after, baseline)

	t.Logf("memory retention [%s]: alloc %+.2f MB (limit %.2f) heap_inuse %+.2f MB sys %+.2f MB (limit %.2f) goroutines %+d",
		budget.label,
		retainedAlloc, budget.maxRetainedAllocMB,
		after.HeapInuseMB-baseline.HeapInuseMB,
		retainedSys, budget.maxRetainedSysMB,
		after.Goroutines-baseline.Goroutines,
	)

	if retainedAlloc > budget.maxRetainedAllocMB {
		t.Errorf("memory not released after %s: alloc retained %+.2f MB exceeds %.2f MB (baseline=%.2f after=%.2f)",
			budget.label, retainedAlloc, budget.maxRetainedAllocMB, baseline.AllocMB, after.AllocMB)
	}
	if retainedSys > budget.maxRetainedSysMB {
		t.Errorf("runtime Sys not released after %s: sys retained %+.2f MB exceeds %.2f MB (baseline=%.2f after=%.2f)",
			budget.label, retainedSys, budget.maxRetainedSysMB, baseline.SysMB, after.SysMB)
	}
}

// runFormatRecordTest exercises a full record/stop cycle with profiling hooks.
func runFormatRecordTest(t *testing.T, profile bilibili.StreamProfile, format string) {
	t.Helper()
	if testing.Short() {
		t.Skipf("skipping %s record test in short mode", format)
	}

	sess := newRecorderTestSession(t)
	roomID := resolveLiveTestRoomID(t, sess.Room)

	baseline := sess.Monitor.snapshotMemory(t, "baseline", true)

	startPhase, err := sess.Monitor.beginPhase(format + "_start")
	if err != nil {
		t.Fatalf("begin start phase: %v", err)
	}
	startErr := sess.Recorder.Start(roomID, recorder.WithStreamOptions(bilibili.WithProfiles(profile)))
	startReport := startPhase.end(t)
	handleRecordingStartErr(t, startErr)
	logCPUPhase(t, startReport)

	outputPath := waitForOutputPathAfterStart(t, sess.Recorder, roomID)

	_ = sess.Monitor.runRecordingProfiledWait(t, format+"_recording", integrationRecordDuration())
	during := sess.Monitor.snapshotMemory(t, "during_recording", false)
	logMemoryDelta(t, baseline, during)

	t.Log("stopping recording")
	t.Logf("stop success: %v", sess.Recorder.Stop(roomID))
	waitUntilNoActiveRecordings(t, sess.Recorder, 30*time.Second)
	time.Sleep(recorderTestSettleAfterStop)

	afterStop := sess.Monitor.snapshotMemory(t, "after_stop", false)
	logMemoryDelta(t, during, afterStop)

	afterCleanup := sess.Monitor.snapshotMemoryReleased(t, format+"_after_cleanup")
	assertRecordingMemoryReleased(t, baseline, afterCleanup, recordingMemoryBudgetForSessions(1, format))

	sess.Monitor.logAnalysisHints(t)

	if checkFFmpegAvailable(t) {
		t.Logf("\n📹 Verifying %s recordings in room dir...", strings.ToUpper(format))
		verifyAllRecordingsInRoomDir(t, filepath.Dir(outputPath), format)
	}
}

type concurrentFormatRecordSpec struct {
	profile bilibili.StreamProfile
	format  string
}

type concurrentRoomRecording struct {
	roomID     int
	outputPath string
	spec       concurrentFormatRecordSpec
}

// runConcurrentFormatRecordTest records each format on a distinct live room in parallel.
// Structure mirrors runFormatRecordTest with profiling, memory budget, and directory ffprobe.
func runConcurrentFormatRecordTest(t *testing.T, specs ...concurrentFormatRecordSpec) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping concurrent format record test in short mode")
	}
	if len(specs) == 0 {
		t.Fatal("no recording specs")
	}

	label := specs[0].format
	for i := 1; i < len(specs); i++ {
		label += "_" + specs[i].format
	}

	sess := newRecorderTestSession(t)
	rooms := resolveLiveTestRoomIDs(t, sess.Room, len(specs))
	if len(rooms) < len(specs) {
		t.Fatalf("need %d live rooms, got %d", len(specs), len(rooms))
	}

	baseline := sess.Monitor.snapshotMemory(t, "concurrent_baseline", true)
	sess.Room.InvalidateRooms(rooms...)

	recordings := make([]concurrentRoomRecording, len(specs))
	for i, spec := range specs {
		recordings[i] = concurrentRoomRecording{
			roomID: rooms[i],
			spec:   spec,
		}
	}

	t.Logf("concurrent format record: label=%s rooms=%v", label, rooms)

	startPhase, err := sess.Monitor.beginPhase(label + "_start")
	if err != nil {
		t.Fatalf("begin start phase: %v", err)
	}

	type startResult struct {
		room int
		err  error
	}

	startGate := make(chan struct{})
	resultCh := make(chan startResult, len(recordings))
	var wg sync.WaitGroup
	for i := range recordings {
		rec := recordings[i]
		spec := specs[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startGate
			err := sess.Recorder.Start(rec.roomID, recorder.WithStreamOptions(bilibili.WithProfiles(spec.profile)))
			resultCh <- startResult{room: rec.roomID, err: err}
		}()
	}
	close(startGate)
	wg.Wait()
	close(resultCh)

	startReport := startPhase.end(t)
	logCPUPhase(t, startReport)

	started := make([]int, 0, len(recordings))
	for r := range resultCh {
		if r.err == nil {
			started = append(started, r.room)
			t.Logf("concurrent start ok: room=%d", r.room)
			continue
		}
		for _, rid := range started {
			_ = sess.Recorder.Stop(rid)
		}
		waitUntilNoActiveRecordings(t, sess.Recorder, 30*time.Second)
		handleRecordingStartErr(t, r.err)
	}

	if len(started) != len(recordings) {
		t.Fatalf("expected %d successful starts, got %d", len(recordings), len(started))
	}

	for i := range recordings {
		recordings[i].outputPath = waitForOutputPathAfterStart(t, sess.Recorder, recordings[i].roomID)
	}

	_ = sess.Monitor.runRecordingProfiledWait(t, label+"_recording", integrationRecordDuration())
	during := sess.Monitor.snapshotMemory(t, "during_recording", false)
	logMemoryDelta(t, baseline, during)

	t.Log("stopping concurrent recordings")
	for _, rec := range recordings {
		t.Logf("stop room=%d success=%v", rec.roomID, sess.Recorder.Stop(rec.roomID))
	}
	waitUntilNoActiveRecordings(t, sess.Recorder, 30*time.Second)
	time.Sleep(recorderTestSettleAfterStop)

	afterStop := sess.Monitor.snapshotMemory(t, "after_stop", false)
	logMemoryDelta(t, during, afterStop)

	afterCleanup := sess.Monitor.snapshotMemoryReleased(t, label+"_after_cleanup")
	assertRecordingMemoryReleased(t, baseline, afterCleanup, recordingMemoryBudgetForSessions(len(specs), label))

	sess.Monitor.logAnalysisHints(t)

	if checkFFmpegAvailable(t) {
		for _, rec := range recordings {
			t.Logf("\n📹 Verifying %s recordings in room dir (room=%d)...", strings.ToUpper(rec.spec.format), rec.roomID)
			verifyAllRecordingsInRoomDir(t, filepath.Dir(rec.outputPath), rec.spec.format)
		}
	}
}
