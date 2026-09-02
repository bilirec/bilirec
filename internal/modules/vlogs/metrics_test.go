package vlogs

import "testing"

func TestNewModuleMetricsUsesVictoriaLogsEnablement(t *testing.T) {
	if moduleMetrics := newModuleMetrics(nil, false); moduleMetrics.enabled {
		t.Fatal("metrics must be disabled when VictoriaLogs is unavailable")
	}
	if moduleMetrics := newModuleMetrics(nil, true); !moduleMetrics.enabled {
		t.Fatal("metrics must be enabled when VictoriaLogs sink is available")
	}
}
