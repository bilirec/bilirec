package stream

import "testing"

func TestDesiredPrefetchWorkersForState(t *testing.T) {
	tests := []struct {
		name                string
		baseWorkers         int
		lowMem              bool
		consecutiveFailures int
		want                int
	}{
		{
			name:                "low-cpu keeps base",
			baseWorkers:         4,
			lowMem:              false,
			consecutiveFailures: 3,
			want:                4,
		},
		{
			name:                "low-mem normal keeps base",
			baseWorkers:         2,
			lowMem:              true,
			consecutiveFailures: 0,
			want:                2,
		},
		{
			name:                "low-mem degrades on failure",
			baseWorkers:         2,
			lowMem:              true,
			consecutiveFailures: 1,
			want:                1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := desiredPrefetchWorkersForState(tc.baseWorkers, tc.lowMem, tc.consecutiveFailures)
			if got != tc.want {
				t.Fatalf("expected workers=%d, got %d", tc.want, got)
			}
		})
	}
}

