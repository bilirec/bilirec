package processors

import "testing"

func TestColdReleaseHotWindow(t *testing.T) {
	if got := coldReleaseHotWindow(8 * 1024 * 1024); got != coldReleaseHotWindowMin {
		t.Fatalf("expected min hot window %d, got %d", coldReleaseHotWindowMin, got)
	}
	if got := coldReleaseHotWindow(32 * 1024 * 1024); got != 128*1024*1024 {
		t.Fatalf("expected 128MB hot window, got %d", got)
	}
}

func TestPlanColdRelease_NotEnoughData(t *testing.T) {
	_, ok := planColdRelease(10*1024*1024, 0, 8*1024*1024)
	if ok {
		t.Fatal("expected no release when onDisk is below hot window")
	}
}

func TestPlanColdRelease_BelowMinRelease(t *testing.T) {
	onDisk := int64(coldReleaseHotWindowMin + 16*1024*1024)
	_, ok := planColdRelease(onDisk, 0, 8*1024*1024)
	if ok {
		t.Fatal("expected no release when cold span is below min release bytes")
	}
}

func TestPlanColdRelease_ReleasesColdPrefix(t *testing.T) {
	onDisk := int64(coldReleaseHotWindowMin + coldReleaseMinRelease)
	plan, ok := planColdRelease(onDisk, 0, 8*1024*1024)
	if !ok {
		t.Fatal("expected release plan")
	}
	if plan.Offset != 0 {
		t.Fatalf("expected offset 0, got %d", plan.Offset)
	}
	if plan.Length != coldReleaseMinRelease {
		t.Fatalf("expected length %d, got %d", coldReleaseMinRelease, plan.Length)
	}
	if plan.NewEnd != int64(coldReleaseMinRelease) {
		t.Fatalf("expected new end %d, got %d", coldReleaseMinRelease, plan.NewEnd)
	}
}

func TestPlanColdRelease_Incremental(t *testing.T) {
	released := int64(coldReleaseMinRelease)
	onDisk := released + coldReleaseHotWindowMin + coldReleaseMinRelease
	plan, ok := planColdRelease(onDisk, released, 8*1024*1024)
	if !ok {
		t.Fatal("expected incremental release plan")
	}
	if plan.Offset != released {
		t.Fatalf("expected offset %d, got %d", released, plan.Offset)
	}
	if plan.Length != coldReleaseMinRelease {
		t.Fatalf("expected length %d, got %d", coldReleaseMinRelease, plan.Length)
	}
}

func TestPeriodicIOConfig_PrefersFsync(t *testing.T) {
	w := &BufferedStreamWriterProcessor{
		syncPeriod:             30,
		coldCacheReleasePeriod: 60,
	}
	mode, period := w.periodicIOConfig()
	if mode != periodicIOFsync || period != 30 {
		t.Fatalf("expected fsync mode, got mode=%v period=%v", mode, period)
	}
}

func TestPeriodicIOConfig_ColdReleaseWhenNoFsync(t *testing.T) {
	w := &BufferedStreamWriterProcessor{
		coldCacheReleasePeriod: 60,
	}
	mode, period := w.periodicIOConfig()
	if mode != periodicIOColdRelease || period != 60 {
		t.Fatalf("expected cold release mode, got mode=%v period=%v", mode, period)
	}
}
