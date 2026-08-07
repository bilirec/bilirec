package subcheck

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/internal/services/notify"
	"github.com/bilirec/bilirec/internal/services/room"
	"github.com/bilirec/bilirec/internal/services/subscribe"
	"github.com/bilirec/bilirec/internal/testutil"
	"github.com/bilirec/bilirec/pkg/db"
	"github.com/puzpuzpuz/xsync/v4"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/process"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

const (
	subcheckTestProfileDirEnv = "SUBCHECK_TEST_PROFILE_DIR"
	subcheckProfileLogTopEnv  = "SUBCHECK_PROFILE_LOG_TOP"
	subcheckLiveRoomsEnv      = "SUBCHECK_LIVE_ROOMS"
	subcheckCPUTopN           = 12
	subcheckLiveRetries       = 2
	subcheckLiveRetryBackoff  = 1500 * time.Millisecond
)

type subcheckTestSession struct {
	app      *fxtest.App
	roomSvc  *room.Service
	subSvc   *subscribe.Service
	notify   *notify.Service
	monitor  *subcheckTestMonitor
	subcheck *Service
}

func TestSubcheck_FullTick_CPUSpike_Live(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live subcheck cpu test in short mode")
	}

	sess := newSubcheckTestSession(t)

	targetRooms := parseTargetRoomCount(t)
	validated := prepareSubscribedLiveRooms(t, sess, targetRooms)

	phaseName := fmt.Sprintf("subcheck_full_tick_before_n%d", targetRooms)
	phase, err := sess.monitor.beginPhase(phaseName, "before")
	if err != nil {
		t.Fatalf("begin cpu phase failed: %v", err)
	}
	sess.subcheck.tryStartAllAutoRecordRooms()
	report := phase.end(t)
	sess.monitor.writeResult(t, "before", len(validated), report)
	sess.monitor.maybeLogTop(t, report.ProfilePath)

	t.Logf(
		"subcheck full tick baseline: rooms=%d wall=%s cpu=%s util=%.2f%% profile=%s",
		len(validated),
		report.Wall,
		report.CPUTime,
		report.UtilPercent,
		report.ProfilePath,
	)
}

func TestSubcheck_ShardTick_CPUSpike_Live(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live subcheck cpu test in short mode")
	}

	sess := newSubcheckTestSession(t)
	targetRooms := parseTargetRoomCount(t)
	validated := prepareSubscribedLiveRooms(t, sess, targetRooms)
	shardCount := computeSchedule(len(validated), scheduleParamsFromConfig(50, 10, 60, 300, 32)).shards

	phaseName := fmt.Sprintf("subcheck_shard_tick_after_n%d_k%d", targetRooms, shardCount)
	phase, err := sess.monitor.beginPhase(phaseName, "after")
	if err != nil {
		t.Fatalf("begin cpu phase failed: %v", err)
	}
	sess.subcheck.tryStartShardAutoRecordRooms(0, shardCount)
	report := phase.end(t)
	sess.monitor.writeResult(t, "after", len(validated), report)
	sess.monitor.maybeLogTop(t, report.ProfilePath)

	t.Logf(
		"subcheck shard tick after: rooms=%d k=%d wall=%s cpu=%s util=%.2f%% profile=%s",
		len(validated),
		shardCount,
		report.Wall,
		report.CPUTime,
		report.UtilPercent,
		report.ProfilePath,
	)
}

func parseTargetRoomCount(tb testing.TB) int {
	tb.Helper()
	raw := strings.TrimSpace(os.Getenv(subcheckLiveRoomsEnv))
	if raw == "" {
		return 120
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		tb.Fatalf("invalid %s value %q", subcheckLiveRoomsEnv, raw)
	}
	return n
}

func prepareSubscribedLiveRooms(t *testing.T, sess *subcheckTestSession, targetRooms int) []int {
	t.Helper()
	validated := validateLiveRoomIDs(t, sess.roomSvc, targetRooms)
	if len(validated) < targetRooms {
		t.Skipf("not enough validated live rooms, want=%d got=%d", targetRooms, len(validated))
	}

	for _, roomID := range validated {
		if err := sess.subSvc.Subscribe(roomID); err != nil && err != subscribe.ErrRoomAlreadySubscribed {
			t.Fatalf("subscribe room %d failed: %v", roomID, err)
		}
		if err := sess.subSvc.UpdateConfig(roomID, &subscribe.RoomConfig{
			Notify:                true,
			AutoRecord:            false,
			RecordDurationMinutes: 0,
		}); err != nil {
			t.Fatalf("update room %d config failed: %v", roomID, err)
		}
	}
	return validated
}

func validateLiveRoomIDs(tb testing.TB, roomSvc *room.Service, required int) []int {
	tb.Helper()
	if required <= 0 {
		return nil
	}

	candidateCount := max(required*3, 60)
	candidates := testutil.LiveRoomIDs(tb, candidateCount)
	uniqueCandidates := uniqueInts(candidates)
	if len(uniqueCandidates) == 0 {
		tb.Skip("no candidate live room ids")
	}

	var details []string
	for round := 1; round <= subcheckLiveRetries+1; round++ {
		roomSvc.InvalidateRooms(uniqueCandidates...)
		infos, err := roomSvc.GetMultipleRoomInfos(uniqueCandidates...)
		if err != nil {
			details = append(details, fmt.Sprintf("round %d fetch failed: %v", round, err))
		} else {
			validated := make([]int, 0, required)
			for _, roomID := range uniqueCandidates {
				info, ok := infos[strconv.Itoa(roomID)]
				if !ok || info == nil {
					details = append(details, fmt.Sprintf("round %d room %d missing info", round, roomID))
					continue
				}
				if info.LiveStatus == 1 {
					validated = append(validated, roomID)
				}
			}
			if len(validated) >= required {
				slices.Sort(validated)
				return validated[:required]
			}
			details = append(details, fmt.Sprintf("round %d validated=%d want=%d", round, len(validated), required))
		}
		if round <= subcheckLiveRetries {
			time.Sleep(subcheckLiveRetryBackoff)
		}
	}

	tb.Skipf("unable to validate %d live rooms; details=%s", required, strings.Join(details, "; "))
	return nil
}

func uniqueInts(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[int]struct{}, len(values))
	result := make([]int, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	return result
}

func newSubcheckTestSession(t *testing.T) *subcheckTestSession {
	t.Helper()
	t.Setenv("DATABASE_DIR", t.TempDir())
	if os.Getenv("CI") != "" {
		t.Setenv("BILIBILI_LOGIN_MODE", "anonymous")
	}

	monitor := newSubcheckTestMonitor(t)

	var roomSvc *room.Service
	var subSvc *subscribe.Service
	var notifySvc *notify.Service

	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Provide(room.NewService),
		fx.Provide(subscribe.NewService),
		fx.Provide(notify.NewService),
		fx.Populate(&roomSvc, &subSvc, &notifySvc),
		fx.StartTimeout(20*time.Second),
	)
	app.RequireStart()
	t.Cleanup(func() {
		app.RequireStop()
	})

	client, err := db.Open(filepath.Join(t.TempDir(), "subcheck-cpu-test.db"))
	if err != nil {
		t.Fatalf("open subcheck test db failed: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})

	bucket, err := client.Bucket(sessionKeysBucketName)
	if err != nil {
		t.Fatalf("open subcheck bucket failed: %v", err)
	}

	service := &Service{
		m:           &metrics.Exporter{},
		subSvc:      subSvc,
		roomSvc:     roomSvc,
		notifySvc:   notifySvc,
		bucket:      bucket,
		sessionKeys: xsync.NewMap[int, string](),
	}

	return &subcheckTestSession{
		app:      app,
		roomSvc:  roomSvc,
		subSvc:   subSvc,
		notify:   notifySvc,
		monitor:  monitor,
		subcheck: service,
	}
}

type subcheckTestMonitor struct {
	proc       *process.Process
	profileDir string
}

func newSubcheckTestMonitor(t *testing.T) *subcheckTestMonitor {
	t.Helper()
	pid := int32(os.Getpid())
	proc, err := process.NewProcess(pid)
	if err != nil {
		t.Fatalf("create process monitor failed: %v", err)
	}

	rootDir := strings.TrimSpace(os.Getenv(subcheckTestProfileDirEnv))
	if rootDir == "" {
		rootDir = filepath.Join(t.TempDir(), "subcheck-profiles")
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		t.Fatalf("create profile root dir failed: %v", err)
	}
	t.Logf("subcheck monitor: profile_dir=%s pid=%d", rootDir, pid)

	return &subcheckTestMonitor{
		proc:       proc,
		profileDir: rootDir,
	}
}

type subcheckCPUPhase struct {
	monitor     *subcheckTestMonitor
	name        string
	profilePath string
	profileFile *os.File
	wallStart   time.Time
	timesStart  *cpu.TimesStat
}

type subcheckCPUPhaseReport struct {
	Name        string
	Wall        time.Duration
	CPUTime     time.Duration
	UtilPercent float64
	ProfilePath string
}

func (m *subcheckTestMonitor) beginPhase(name string, set string) (*subcheckCPUPhase, error) {
	times, err := m.proc.Times()
	if err != nil {
		return nil, err
	}

	dir := filepath.Join(m.profileDir, set)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	profilePath := filepath.Join(dir, sanitizeProfileLabel(name)+".pprof")
	f, err := os.Create(profilePath)
	if err != nil {
		return nil, err
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		_ = f.Close()
		return nil, err
	}

	return &subcheckCPUPhase{
		monitor:     m,
		name:        name,
		profilePath: profilePath,
		profileFile: f,
		wallStart:   time.Now(),
		timesStart:  times,
	}, nil
}

func (p *subcheckCPUPhase) end(t *testing.T) subcheckCPUPhaseReport {
	t.Helper()
	pprof.StopCPUProfile()
	_ = p.profileFile.Close()

	report := subcheckCPUPhaseReport{
		Name:        p.name,
		Wall:        time.Since(p.wallStart),
		ProfilePath: p.profilePath,
	}

	timesEnd, err := p.monitor.proc.Times()
	if err != nil {
		t.Logf("read process times failed: %v", err)
		return report
	}
	cpuSeconds := timesEnd.Total() - p.timesStart.Total()
	report.CPUTime = time.Duration(cpuSeconds * float64(time.Second))
	report.UtilPercent = cpuUtilizationPercent(report.CPUTime, report.Wall)
	return report
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
		return "profile"
	}
	return string(out)
}

func (m *subcheckTestMonitor) maybeLogTop(t *testing.T, profilePath string) {
	t.Helper()
	if strings.TrimSpace(os.Getenv(subcheckProfileLogTopEnv)) == "" {
		return
	}
	cmd := exec.Command(
		"go",
		"tool",
		"pprof",
		"-nodecount="+strconv.Itoa(subcheckCPUTopN),
		"-top",
		profilePath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("pprof top failed: %v\n%s", err, string(out))
		return
	}
	t.Logf("pprof top %d (%s):\n%s", subcheckCPUTopN, filepath.Base(profilePath), string(out))
}

type subcheckCPUResult struct {
	Set         string  `json:"set"`
	Rooms       int     `json:"rooms"`
	ProfilePath string  `json:"profile_path"`
	WallMS      int64   `json:"wall_ms"`
	CPUTimeMS   int64   `json:"cpu_time_ms"`
	UtilPercent float64 `json:"util_percent"`
	Timestamp   string  `json:"timestamp"`
}

func (m *subcheckTestMonitor) writeResult(t *testing.T, set string, rooms int, report subcheckCPUPhaseReport) {
	t.Helper()
	result := subcheckCPUResult{
		Set:         set,
		Rooms:       rooms,
		ProfilePath: report.ProfilePath,
		WallMS:      report.Wall.Milliseconds(),
		CPUTimeMS:   report.CPUTime.Milliseconds(),
		UtilPercent: report.UtilPercent,
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Logf("marshal subcheck cpu result failed: %v", err)
		return
	}
	resultsPath := filepath.Join(m.profileDir, set, "results.jsonl")
	f, err := os.OpenFile(resultsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Logf("open results file failed: %v", err)
		return
	}
	defer func() {
		_ = f.Close()
	}()
	if _, err := f.Write(append(data, '\n')); err != nil {
		t.Logf("write results file failed: %v", err)
	}
}
