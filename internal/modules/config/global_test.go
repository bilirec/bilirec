package config

import "testing"

func TestLiveStreamWriterColdCacheReleaseSecs_WithSync(t *testing.T) {
	g := &GlobalReadOnly{config: &Config{
		BilibiliLoginMode:                      "controller",
		RecordingRecoveryDuration:              "preserve",
		liveStreamWriterSyncPeriod:             45,
		liveStreamWriterColdCacheReleasePeriod: 60,
	}}
	if err := g.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := g.LiveStreamWriterColdCacheReleaseSecs(); got != 60 {
		t.Fatalf("expected configured 60, got %d", got)
	}
}

func TestLiveStreamWriterColdCacheReleaseSecs_OnlyCold(t *testing.T) {
	g := &GlobalReadOnly{config: &Config{
		liveStreamWriterSyncPeriod:             0,
		liveStreamWriterColdCacheReleasePeriod: 60,
	}}
	if got := g.LiveStreamWriterColdCacheReleaseSecs(); got != 60 {
		t.Fatalf("expected 60, got %d", got)
	}
}

func TestLiveStreamWriterColdCacheReleaseSecs_NegativeCold(t *testing.T) {
	g := &GlobalReadOnly{config: &Config{
		liveStreamWriterSyncPeriod:             0,
		liveStreamWriterColdCacheReleasePeriod: -1,
	}}
	if got := g.LiveStreamWriterColdCacheReleaseSecs(); got != 0 {
		t.Fatalf("expected 0 for negative cold period, got %d", got)
	}
}

func TestServeCacheIdleReleaseSecs_ZeroUsesDefault(t *testing.T) {
	g := &GlobalReadOnly{config: &Config{liveStreamWriterColdCacheReleasePeriod: 0}}
	if got := g.ServeCacheIdleReleaseSecs(); got != 60 {
		t.Fatalf("expected default 60 when cold=0, got %d", got)
	}
}

func TestServeCacheIdleReleaseSecs_ConfiguredValue(t *testing.T) {
	g := &GlobalReadOnly{config: &Config{liveStreamWriterColdCacheReleasePeriod: 120}}
	if got := g.ServeCacheIdleReleaseSecs(); got != 120 {
		t.Fatalf("expected 120, got %d", got)
	}
}

func TestDropFilePageCache_DefaultEnabled(t *testing.T) {
	g := &GlobalReadOnly{config: &Config{dropFilePageCache: true}}
	if !g.DropFilePageCache() {
		t.Fatal("expected drop file page cache enabled by default")
	}
}

func TestValidate_RecordingRecoveryDuration_Invalid(t *testing.T) {
	g := &GlobalReadOnly{config: &Config{
		BilibiliLoginMode:         "controller",
		RecordingRecoveryDuration: "invalid",
	}}
	if err := g.Validate(); err == nil {
		t.Fatal("expected Validate error for invalid RECORDING_RECOVERY_DURATION")
	}
}

func TestValidate_RecordingRecoveryDuration_Valid(t *testing.T) {
	for _, mode := range []string{"preserve", "reset"} {
		g := &GlobalReadOnly{config: &Config{
			BilibiliLoginMode:         "controller",
			RecordingRecoveryDuration: mode,
		}}
		if err := g.Validate(); err != nil {
			t.Fatalf("Validate(%q): %v", mode, err)
		}
	}
}
