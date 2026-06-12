package config

import "testing"

func TestPerfPreset_DefaultsToLowCPU(t *testing.T) {
	g := &GlobalReadOnly{config: &Config{perfPreset: ""}}

	if g.PerfPreset() != perfPresetLowCPU {
		t.Fatalf("expected default perf preset %q, got %q", perfPresetLowCPU, g.PerfPreset())
	}
	if !g.IsLowCPUPreset() {
		t.Fatal("expected IsLowCPUPreset to be true")
	}
	if g.IsLowMemPreset() {
		t.Fatal("expected IsLowMemPreset to be false")
	}
	if got := g.HlsSegmentFetchWorkers(); got != 4 {
		t.Fatalf("expected hls workers 4 for low-cpu, got %d", got)
	}
}

func TestPerfPreset_LowMemWorkers(t *testing.T) {
	g := &GlobalReadOnly{config: &Config{perfPreset: perfPresetLowMem}}

	if g.PerfPreset() != perfPresetLowMem {
		t.Fatalf("expected perf preset %q, got %q", perfPresetLowMem, g.PerfPreset())
	}
	if g.IsLowCPUPreset() {
		t.Fatal("expected IsLowCPUPreset to be false")
	}
	if !g.IsLowMemPreset() {
		t.Fatal("expected IsLowMemPreset to be true")
	}
	if got := g.HlsSegmentFetchWorkers(); got != 2 {
		t.Fatalf("expected hls workers 2 for low-mem, got %d", got)
	}
}

func TestValidateRejectsInvalidPerfPreset(t *testing.T) {
	g := &GlobalReadOnly{config: &Config{
		BilibiliLoginMode: "controller",
		perfPreset:        "fastest",
	}}

	if err := g.Validate(); err == nil {
		t.Fatal("expected Validate to reject invalid PERF_PRESET")
	}
}
