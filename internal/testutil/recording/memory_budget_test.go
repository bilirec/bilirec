package recording

import (
	"runtime"
	"testing"
)

func TestHeapHeldBytes(t *testing.T) {
	tests := []struct {
		name     string
		heapSys  uint64
		released uint64
		want     uint64
	}{
		{name: "nothing released", heapSys: 128 << 20, want: 128 << 20},
		{name: "partial release", heapSys: 128 << 20, released: 64 << 20, want: 64 << 20},
		{name: "all released", heapSys: 128 << 20, released: 128 << 20, want: 0},
		{name: "released exceeds sys", heapSys: 64 << 20, released: 128 << 20, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := runtime.MemStats{HeapSys: tt.heapSys, HeapReleased: tt.released}
			if got := heapHeldBytes(&ms); got != tt.want {
				t.Errorf("heapHeldBytes() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMemoryBudgetForSessionsSingleSession(t *testing.T) {
	budget := MemoryBudgetForSessions(1, "ts")

	if budget.maxRetainedAllocMB != 30 {
		t.Errorf("maxRetainedAllocMB = %.2f, want 30", budget.maxRetainedAllocMB)
	}
	if budget.maxRetainedHeapHeldMB != 40 {
		t.Errorf("maxRetainedHeapHeldMB = %.2f, want 40", budget.maxRetainedHeapHeldMB)
	}
}

func TestMemoryBudgetForSessionsScalesPerSession(t *testing.T) {
	budget := MemoryBudgetForSessions(3, "concurrent")

	if budget.maxRetainedAllocMB != 54 {
		t.Errorf("maxRetainedAllocMB = %.2f, want 54", budget.maxRetainedAllocMB)
	}
	if budget.maxRetainedHeapHeldMB != 72 {
		t.Errorf("maxRetainedHeapHeldMB = %.2f, want 72", budget.maxRetainedHeapHeldMB)
	}
}

func TestMemoryRetentionViolations(t *testing.T) {
	budget := MemoryBudgetForSessions(1, "ts")

	tests := []struct {
		name         string
		baseline     MemorySnapshot
		after        MemorySnapshot
		wantMessages int
	}{
		{
			name:     "sys high water alone does not fail",
			baseline: MemorySnapshot{AllocMB: 2, HeapHeldMB: 20, SysMB: 22},
			after:    MemorySnapshot{AllocMB: 7, HeapHeldMB: 25, SysMB: 126},
		},
		{
			name:         "heap held over limit fails",
			baseline:     MemorySnapshot{AllocMB: 2, HeapHeldMB: 20},
			after:        MemorySnapshot{AllocMB: 7, HeapHeldMB: 120},
			wantMessages: 1,
		},
		{
			name:         "alloc over limit fails",
			baseline:     MemorySnapshot{AllocMB: 2, HeapHeldMB: 20},
			after:        MemorySnapshot{AllocMB: 102, HeapHeldMB: 25},
			wantMessages: 1,
		},
		{
			name:         "alloc and heap held over limit both fail",
			baseline:     MemorySnapshot{AllocMB: 2, HeapHeldMB: 20},
			after:        MemorySnapshot{AllocMB: 102, HeapHeldMB: 120},
			wantMessages: 2,
		},
		{
			name:     "memory freed below baseline passes",
			baseline: MemorySnapshot{AllocMB: 30, HeapHeldMB: 60},
			after:    MemorySnapshot{AllocMB: 5, HeapHeldMB: 10},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := memoryRetentionViolations(tt.baseline, tt.after, budget)
			if len(got) != tt.wantMessages {
				t.Fatalf("violations = %d (%v), want %d", len(got), got, tt.wantMessages)
			}
		})
	}
}

func TestMemoryBudgetForSessionsEnvOverride(t *testing.T) {
	t.Setenv(recorderTestMaxRetainedHeapHeldMBEnv, "220")

	budget := MemoryBudgetForSessions(3, "concurrent")
	if budget.maxRetainedHeapHeldMB != 220 {
		t.Errorf("maxRetainedHeapHeldMB = %.2f, want 220", budget.maxRetainedHeapHeldMB)
	}
}

func TestMemoryBudgetForSessionsPerRoomOverride(t *testing.T) {
	t.Setenv(recorderTestMaxRetainedHeapHeldPerRoomEnv, "30")

	budget := MemoryBudgetForSessions(3, "concurrent")
	if budget.maxRetainedHeapHeldMB != 114 {
		t.Errorf("maxRetainedHeapHeldMB = %.2f, want 114", budget.maxRetainedHeapHeldMB)
	}
}

func TestMemoryBudgetForSessionsPerRoomWinsOverAbsolute(t *testing.T) {
	t.Setenv(recorderTestMaxRetainedHeapHeldMBEnv, "220")
	t.Setenv(recorderTestMaxRetainedHeapHeldPerRoomEnv, "30")

	budget := MemoryBudgetForSessions(3, "concurrent")
	if budget.maxRetainedHeapHeldMB != 114 {
		t.Errorf("maxRetainedHeapHeldMB = %.2f, want 114", budget.maxRetainedHeapHeldMB)
	}
}
