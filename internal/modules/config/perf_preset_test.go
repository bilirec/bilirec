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

func TestHighPoolSizeAccessorsFallbackAndOverride(t *testing.T) {
	g := &GlobalReadOnly{config: &Config{}}
	if got := g.ReadStreamBytesPoolSizeHigh(); got != defaultReadStreamBytesPoolSizeHigh {
		t.Fatalf("expected read high default %d, got %d", defaultReadStreamBytesPoolSizeHigh, got)
	}
	if got := g.LiveStreamWriterBytesPoolSizeHigh(); got != defaultLiveStreamWriterBytesPoolSizeHigh {
		t.Fatalf("expected writer high default %d, got %d", defaultLiveStreamWriterBytesPoolSizeHigh, got)
	}

	g = &GlobalReadOnly{config: &Config{
		readStreamBytesPoolSizeHigh:       2 * 1024 * 1024,
		liveStreamWriterBytesPoolSizeHigh: 1536 * 1024,
	}}
	if got := g.ReadStreamBytesPoolSizeHigh(); got != 2*1024*1024 {
		t.Fatalf("expected read high override 2MB, got %d", got)
	}
	if got := g.LiveStreamWriterBytesPoolSizeHigh(); got != 1536*1024 {
		t.Fatalf("expected writer high override 1536KB, got %d", got)
	}
}

func TestIsHighQualityQn(t *testing.T) {
	if IsHighQualityQn(10000) {
		t.Fatal("expected qn=10000 not high quality")
	}
	if !IsHighQualityQn(20000) {
		t.Fatal("expected qn=20000 to be high quality")
	}
	if !IsHighQualityQn(30000) {
		t.Fatal("expected qn=30000 to be high quality")
	}
}
