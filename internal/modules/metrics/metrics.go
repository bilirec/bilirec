package metrics

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"

	vm "github.com/VictoriaMetrics/metrics"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/updatecheck"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

const (
	labelRoomID  = "room_id"
	labelUname   = "uname"
	labelVersion = "version"

	metricActiveRecordings = "bilirec_active_recordings"

	metricRoomStreamBytesTotal       = "bilirec_room_stream_bytes_total"
	metricRoomRecordingSessionsTotal = "bilirec_room_recording_sessions_total"
	metricRoomLiveSessionsTotal      = "bilirec_room_live_sessions_total"
	metricRoomStreamRecoveryTotal    = "bilirec_room_stream_recovery_total"
	metricRoomRecordingActive        = "bilirec_room_recording_active"
	metricRoomLiveStatus             = "bilirec_room_live_status"
	metricRoomInfo                   = "bilirec_room_info"

	metricGoGoroutines               = "go_goroutines"
	metricGoMemstatsHeapInuseBytes   = "go_memstats_heap_inuse_bytes"
	metricGoMemstatsStackInuseBytes  = "go_memstats_stack_inuse_bytes"
	metricGoMemstatsSysBytes         = "go_memstats_sys_bytes"
	metricGoGCPauseTotalSeconds      = "go_gc_pause_total_seconds"
	metricGoGCNumTotal               = "go_gc_num_total"
	metricProcessResidentMemoryBytes = "process_resident_memory_bytes"
	metricProcessCPUSecondsTotal     = "process_cpu_seconds_total"
	metricProcessThreads             = "process_threads"
	metricProcessOpenFDs             = "process_open_fds"
	metricBuildInfo                  = "bilirec_build_info"
)

var logger = logrus.WithField("module", "metrics")

// Exporter 持有 bilirec 所有 metrics，並可選地透過獨立 port 暴露 /metrics。
// 零值（set == nil）是 no-op：所有 method 立即返回，關閉時零成本。
type Exporter struct {
	set *vm.Set
	cfg *config.Config

	mu    sync.Mutex // 保護 rooms 建立與 room_info 改名重建
	rooms sync.Map   // int -> *roomSeries

	activeRecordings *vm.Gauge
}

// roomSeries 持有單一房間的所有 metric handle，避免熱路徑重複組 metric 字串。
type roomSeries struct {
	streamBytes       *vm.Counter
	recordingSessions *vm.Counter
	liveSessions      *vm.Counter
	recoveries        *vm.Counter
	recordingActive   *vm.Gauge
	liveStatus        *vm.Gauge

	infoName string // bilirec_room_info 的完整 metric 名（改名時 unregister 用）
	uname    string
}

func provider(lc fx.Lifecycle, cfg *config.Config) *Exporter {
	if !cfg.MetricsEnabled {
		return &Exporter{}
	}
	e := &Exporter{set: vm.NewSet(), cfg: cfg}
	e.activeRecordings = e.set.NewGauge(metricActiveRecordings, nil)
	e.registerRuntimeGauges()
	e.registerServer(lc)
	return e
}

// AddStreamBytes 累加房間的串流寫入位元組數（pipeline 輸入側，≈ 落盤量）。
func (e *Exporter) AddStreamBytes(roomID int, n int) {
	if e.set == nil {
		return
	}
	e.room(roomID).streamBytes.Add(n)
}

// RecordingStarted 記錄錄製場次 +1 並標記錄製中。recovery 恢復不應呼叫（同一場錄製的延續）。
func (e *Exporter) RecordingStarted(roomID int, uname string) {
	if e.set == nil {
		return
	}
	rs := e.room(roomID)
	rs.recordingSessions.Inc()
	rs.recordingActive.Set(1)
	e.activeRecordings.Add(1)
	e.updateRoomInfo(rs, roomID, uname)
}

// RecordingStopped 標記錄製結束。不結算時長——每日錄製時間由查詢端對 recording_active 積分得出。
func (e *Exporter) RecordingStopped(roomID int) {
	if e.set == nil {
		return
	}
	e.room(roomID).recordingActive.Set(0)
	e.activeRecordings.Add(-1)
}

// SetLiveStatus 每輪檢查無條件更新開播狀態（自愈：重啟後不需依賴開播事件），並順帶更新 room_info。
func (e *Exporter) SetLiveStatus(roomID int, uname string, live bool) {
	if e.set == nil {
		return
	}
	rs := e.room(roomID)
	if live {
		rs.liveStatus.Set(1)
	} else {
		rs.liveStatus.Set(0)
	}
	e.updateRoomInfo(rs, roomID, uname)
}

// LiveSessionDetected 記錄開播場次 +1（偵測到新直播 session 時呼叫）。
func (e *Exporter) LiveSessionDetected(roomID int) {
	if e.set == nil {
		return
	}
	e.room(roomID).liveSessions.Inc()
}

// AddRecovery 記錄一次斷線恢復嘗試失敗（不含 ErrStreamNotLive 的正常下播重試）。
func (e *Exporter) AddRecovery(roomID int) {
	if e.set == nil {
		return
	}
	e.room(roomID).recoveries.Inc()
}

// DeleteRoom 移除房間的所有 series（例如退訂或不再被追蹤時），防止歷史 series 永久殘留。
func (e *Exporter) DeleteRoom(roomID int) {
	if e.set == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	v, ok := e.rooms.LoadAndDelete(roomID)
	if !ok {
		return
	}
	rs := v.(*roomSeries)
	label := roomLabel(roomID)
	e.set.UnregisterMetric(metricRoomStreamBytesTotal + label)
	e.set.UnregisterMetric(metricRoomRecordingSessionsTotal + label)
	e.set.UnregisterMetric(metricRoomLiveSessionsTotal + label)
	e.set.UnregisterMetric(metricRoomStreamRecoveryTotal + label)
	e.set.UnregisterMetric(metricRoomRecordingActive + label)
	e.set.UnregisterMetric(metricRoomLiveStatus + label)
	if rs.infoName != "" {
		e.set.UnregisterMetric(rs.infoName)
	}
}

func roomLabel(roomID int) string {
	return fmt.Sprintf(`{%s="%d"}`, labelRoomID, roomID)
}

func (e *Exporter) newRoomSeries(roomID int) *roomSeries {
	label := roomLabel(roomID)
	return &roomSeries{
		streamBytes:       e.set.NewCounter(metricRoomStreamBytesTotal + label),
		recordingSessions: e.set.NewCounter(metricRoomRecordingSessionsTotal + label),
		liveSessions:      e.set.NewCounter(metricRoomLiveSessionsTotal + label),
		recoveries:        e.set.NewCounter(metricRoomStreamRecoveryTotal + label),
		recordingActive:   e.set.NewGauge(metricRoomRecordingActive+label, nil),
		liveStatus:        e.set.NewGauge(metricRoomLiveStatus+label, nil),
	}
}

func (e *Exporter) room(roomID int) *roomSeries {
	if rs, ok := e.rooms.Load(roomID); ok {
		return rs.(*roomSeries)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if rs, ok := e.rooms.Load(roomID); ok {
		return rs.(*roomSeries)
	}
	rs := e.newRoomSeries(roomID)
	e.rooms.Store(roomID, rs)
	return rs
}

// updateRoomInfo 維護 bilirec_room_info{room_id,uname}=1 series；
// 改名時重建（歷史區間在 TSDB 中仍 join 出當時的名字，語義正確）。
func (e *Exporter) updateRoomInfo(rs *roomSeries, roomID int, uname string) {
	if uname == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if rs.uname == uname {
		return
	}
	if rs.infoName != "" {
		e.set.UnregisterMetric(rs.infoName)
	}
	rs.infoName = fmt.Sprintf(
		`%s{%s="%d",%s=%s}`,
		metricRoomInfo,
		labelRoomID,
		roomID,
		labelUname,
		strconv.Quote(uname),
	)
	rs.uname = uname
	e.set.NewGauge(rs.infoName, func() float64 { return 1 })
}

// registerRuntimeGauges 註冊資源用量指標，callback 在 scrape 時即興求值，無背景 goroutine。
func (e *Exporter) registerRuntimeGauges() {
	s := e.set
	s.NewGauge(metricGoGoroutines, func() float64 { return float64(runtime.NumGoroutine()) })
	s.NewGauge(metricGoMemstatsHeapInuseBytes, func() float64 {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return float64(ms.HeapInuse)
	})
	s.NewGauge(metricGoMemstatsStackInuseBytes, func() float64 {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return float64(ms.StackInuse)
	})
	s.NewGauge(metricGoMemstatsSysBytes, func() float64 {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return float64(ms.Sys)
	})
	s.NewGauge(metricGoGCPauseTotalSeconds, func() float64 {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return float64(ms.PauseTotalNs) / 1e9
	})
	s.NewGauge(metricGoGCNumTotal, func() float64 {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return float64(ms.NumGC)
	})

	proc, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		logger.Warnf("无法创建 process handle，跳过 process 指标：%v", err)
		return
	}
	s.NewGauge(metricProcessResidentMemoryBytes, func() float64 {
		mi, err := proc.MemoryInfo()
		if err != nil {
			return 0
		}
		return float64(mi.RSS)
	})
	s.NewGauge(metricProcessCPUSecondsTotal, func() float64 {
		t, err := proc.Times()
		if err != nil {
			return 0
		}
		return t.User + t.System
	})
	s.NewGauge(metricProcessThreads, func() float64 {
		n, err := proc.NumThreads()
		if err != nil {
			return 0
		}
		return float64(n)
	})
	// NumFDs 僅部分平台支援（Linux/Android），啟動時探測，不支援就不註冊
	if _, err := proc.NumFDs(); err == nil {
		s.NewGauge(metricProcessOpenFDs, func() float64 {
			n, err := proc.NumFDs()
			if err != nil {
				return 0
			}
			return float64(n)
		})
	}

	if v := updatecheck.Current(); v != "" {
		s.NewGauge(
			fmt.Sprintf(`%s{%s=%s}`, metricBuildInfo, labelVersion, strconv.Quote(v)),
			func() float64 { return 1 },
		)
	}
}

var Module = fx.Module("metrics", fx.Provide(provider))
