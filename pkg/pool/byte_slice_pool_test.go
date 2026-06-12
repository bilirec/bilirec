package pool

import "testing"

func TestByteSlicePool_GetSized_NormalizesNegative(t *testing.T) {
	pools := []struct {
		name string
		pool ByteSlicePool
	}{
		{name: "bucketed", pool: NewBucketedBytesPool(256 * 1024)},
		{name: "slice", pool: NewBytesSlicePool(256*1024, 256*1024)},
	}

	for _, tc := range pools {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.pool.GetSized(-1)
			if len(got) != 0 {
				t.Fatalf("expected len=0 for negative size, got %d", len(got))
			}
			tc.pool.Put(got)
		})
	}
}

func TestByteSlicePool_GetSized_AndPutRoundtrip(t *testing.T) {
	pools := []struct {
		name string
		pool ByteSlicePool
		size int
	}{
		{name: "bucketed_small", pool: NewBucketedBytesPool(256 * 1024), size: 64 * 1024},
		{name: "bucketed_mid", pool: NewBucketedBytesPool(256 * 1024), size: 256 * 1024},
		{name: "slice_fixed", pool: NewBytesSlicePool(256*1024, 256*1024), size: 128 * 1024},
	}

	for _, tc := range pools {
		t.Run(tc.name, func(t *testing.T) {
			buf := tc.pool.GetSized(tc.size)
			if len(buf) != tc.size {
				t.Fatalf("expected len=%d, got %d", tc.size, len(buf))
			}
			tc.pool.Put(buf)

			next := tc.pool.GetSized(tc.size)
			if len(next) != tc.size {
				t.Fatalf("expected len=%d after put/get, got %d", tc.size, len(next))
			}
			tc.pool.Put(next)
		})
	}
}

func TestByteSlicePool_OversizedRequest(t *testing.T) {
	pools := []struct {
		name string
		pool ByteSlicePool
	}{
		{name: "bucketed", pool: NewBucketedBytesPool(256 * 1024)},
		{name: "slice", pool: NewBytesSlicePool(256*1024, 256*1024)},
	}

	oversized := 6 * 1024 * 1024
	for _, tc := range pools {
		t.Run(tc.name, func(t *testing.T) {
			buf := tc.pool.GetSized(oversized)
			if len(buf) != oversized {
				t.Fatalf("expected oversized len=%d, got %d", oversized, len(buf))
			}
			tc.pool.Put(buf) // should be safe even when oversized is not retained
		})
	}
}

