package convert

import "testing"

func TestNewServiceMetricsUsesConversionEnablement(t *testing.T) {
	disabled := newServiceMetrics(nil, false)
	if disabled.enabled {
		t.Fatal("metrics must be disabled when conversion is disabled")
	}
	disabled.setTaskMetrics(ProviderFFmpeg, 1, 1)

	if metrics := newServiceMetrics(nil, true); !metrics.enabled {
		t.Fatal("metrics must be enabled when conversion and a manager are available")
	}
}
