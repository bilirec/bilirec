package metrics

import (
	"fmt"
	"os"
	"runtime"
	"strconv"

	"github.com/bilirec/bilirec/pkg/updatecheck"
	"github.com/shirou/gopsutil/v4/process"
)

const (
	labelVersion = "version"

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

// registerRuntimeGauges registers callback gauges that are evaluated during
// scrape, without starting background polling goroutines.
func (e *Exporter) registerRuntimeGauges() {
	if e.set == nil {
		return
	}

	s := e.set
	s.NewGauge(metricGoGoroutines, func() float64 { return float64(runtime.NumGoroutine()) })
	s.NewGauge(metricGoMemstatsHeapInuseBytes, func() float64 {
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)
		return float64(memStats.HeapInuse)
	})
	s.NewGauge(metricGoMemstatsStackInuseBytes, func() float64 {
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)
		return float64(memStats.StackInuse)
	})
	s.NewGauge(metricGoMemstatsSysBytes, func() float64 {
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)
		return float64(memStats.Sys)
	})
	s.NewGauge(metricGoGCPauseTotalSeconds, func() float64 {
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)
		return float64(memStats.PauseTotalNs) / 1e9
	})
	s.NewGauge(metricGoGCNumTotal, func() float64 {
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)
		return float64(memStats.NumGC)
	})

	proc, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		logger.Warnf("无法创建 process handle，跳过 process 指标：%v", err)
		return
	}
	s.NewGauge(metricProcessResidentMemoryBytes, func() float64 {
		memInfo, err := proc.MemoryInfo()
		if err != nil {
			return 0
		}
		return float64(memInfo.RSS)
	})
	s.NewGauge(metricProcessCPUSecondsTotal, func() float64 {
		times, err := proc.Times()
		if err != nil {
			return 0
		}
		return times.User + times.System
	})
	s.NewGauge(metricProcessThreads, func() float64 {
		threads, err := proc.NumThreads()
		if err != nil {
			return 0
		}
		return float64(threads)
	})
	// NumFDs is supported only on some platforms (Linux/Android). Probe once
	// at startup and omit the metric when unsupported.
	if _, err := proc.NumFDs(); err == nil {
		s.NewGauge(metricProcessOpenFDs, func() float64 {
			fds, err := proc.NumFDs()
			if err != nil {
				return 0
			}
			return float64(fds)
		})
	}

	if version := updatecheck.Current(); version != "" {
		s.NewGauge(
			fmt.Sprintf(`%s{%s=%s}`, metricBuildInfo, labelVersion, strconv.Quote(version)),
			func() float64 { return 1 },
		)
	}
}
