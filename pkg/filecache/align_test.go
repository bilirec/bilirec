package filecache

import "testing"

func TestAlignDown(t *testing.T) {
	tests := []struct {
		in, want int64
	}{
		{0, 0},
		{1, 0},
		{4095, 0},
		{4096, 4096},
		{8192, 8192},
		{9000, 8192},
	}
	for _, tc := range tests {
		if got := alignDown(tc.in); got != tc.want {
			t.Fatalf("alignDown(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestAlignedColdRange(t *testing.T) {
	off, length, ok := alignedColdRange(100, 5000)
	if !ok {
		t.Fatal("expected ok")
	}
	if off != 0 || length != 4096 {
		t.Fatalf("got off=%d length=%d", off, length)
	}

	_, _, ok = alignedColdRange(100, 0)
	if ok {
		t.Fatal("expected not ok for zero length")
	}

	_, _, ok = alignedColdRange(4096, 100)
	if ok {
		t.Fatal("expected not ok when aligned range is empty")
	}
}
