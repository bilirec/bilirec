package metrics

import (
	"fmt"
	"io"
	"strconv"

	vm "github.com/VictoriaMetrics/metrics"
	"github.com/bilirec/bilirec/pkg/updatecheck"
)

const (
	labelVersion    = "version"
	metricBuildInfo = "bilirec_build_info"
)

// registerRuntimeGauges registers scrape-time metrics that belong to the
// process itself. Room series stay on e.set; Go / process series are appended
// by VictoriaMetrics/metrics during the same Set.WritePrometheus call.
func (e *Exporter) registerRuntimeGauges() {
	if e.set == nil {
		return
	}

	// use VictoriaMetrics official process metrics instead of our own
	e.set.RegisterMetricsWriter(func(w io.Writer) {
		vm.WriteProcessMetrics(w)
		vm.WriteFDMetrics(w)
	})

	if version := updatecheck.Current(); version != "" {
		e.set.NewGauge(
			fmt.Sprintf(`%s{%s=%s}`, metricBuildInfo, labelVersion, strconv.Quote(version)),
			func() float64 { return 1 },
		)
	}
}
