package config

import "testing"

func TestLiveStreamWriterColdCacheReleaseSecs_WithSync(t *testing.T) {
	g := &GlobalReadOnly{config: &Config{
		BilibiliLoginMode:                      "controller",
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

func TestDropFilePageCache_DefaultEnabled(t *testing.T) {
	g := &GlobalReadOnly{config: &Config{dropFilePageCache: true}}
	if !g.DropFilePageCache() {
		t.Fatal("expected drop file page cache enabled by default")
	}
}
