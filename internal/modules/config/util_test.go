package config

import "testing"

func TestReadStreamBytesPoolBoundedCapacity(t *testing.T) {
	tests := []struct {
		chanBuf  int
		expected int
	}{
		{0, 4},
		{16, 4},
		{48, 12},
		{100, 16},
		{1, 2},
	}
	for _, tc := range tests {
		if got := ReadStreamBytesPoolBoundedCapacity(tc.chanBuf); got != tc.expected {
			t.Fatalf("chan=%d: expected %d, got %d", tc.chanBuf, tc.expected, got)
		}
	}
}

func TestLiveStreamWriterBytesPoolBoundedCapacityPerBucket(t *testing.T) {
	tests := []struct {
		chanBuf  int
		expected int
	}{
		{0, 8},
		{64, 8},
		{128, 8},
		{16, 2},
		{24, 3},
	}
	for _, tc := range tests {
		if got := LiveStreamWriterBytesPoolBoundedCapacityPerBucket(tc.chanBuf); got != tc.expected {
			t.Fatalf("chan=%d: expected %d, got %d", tc.chanBuf, tc.expected, got)
		}
	}
}
