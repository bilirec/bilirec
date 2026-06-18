package pool

import (
	"testing"

	"github.com/bilirec/bilirec/pkg/benchreport"
)

func benchmarkBytesPoolGetPut(b *testing.B, size, capacity int, bounded bool) {
	b.Helper()
	var opts []BytesPoolOption
	if bounded {
		opts = []BytesPoolOption{
			WithPoolBoundedMode(true),
			WithPoolBoundedCapacity(capacity),
		}
	}
	p := NewBytesPool(size, opts...)
	mon := benchreport.Start(b, int64(size))
	b.ReportAllocs()
	b.SetBytes(int64(size))
	b.ResetTimer()
	mon.MarkTimerStart()
	for i := 0; i < b.N; i++ {
		buf := p.GetBytes()
		p.PutBytes(buf)
		mon.SamplePeriodically(i)
	}
}

func BenchmarkBytesPool_GetPut(b *testing.B) {
	size := 512 * 1024
	b.Run("soft", func(b *testing.B) {
		benchmarkBytesPoolGetPut(b, size, 0, false)
	})
	b.Run("bounded_cap4", func(b *testing.B) {
		benchmarkBytesPoolGetPut(b, size, 4, true)
	})
	b.Run("bounded_cap16", func(b *testing.B) {
		benchmarkBytesPoolGetPut(b, size, 16, true)
	})
}

func benchmarkBucketedBytesPoolGetPut(b *testing.B, base, size, capacity int, bounded bool) {
	b.Helper()
	var opts []BucketedBytesPoolOption
	if bounded {
		opts = []BucketedBytesPoolOption{
			WithPoolBoundedMode(true),
			WithPoolBoundedCapacity(capacity),
		}
	}
	p := NewBucketedBytesPool(base, opts...)
	mon := benchreport.Start(b, int64(size))
	b.ReportAllocs()
	b.SetBytes(int64(size))
	b.ResetTimer()
	mon.MarkTimerStart()
	for i := 0; i < b.N; i++ {
		buf := p.GetSized(size)
		p.Put(buf)
		mon.SamplePeriodically(i)
	}
}

func BenchmarkBucketedBytesPool_GetPut(b *testing.B) {
	cases := []struct {
		name     string
		base     int
		size     int
		capacity int
		bounded  bool
	}{
		{name: "soft_base512_hit", base: 512 * 1024, size: 256 * 1024, bounded: false},
		{name: "soft_base512_edge", base: 512 * 1024, size: 512 * 1024, bounded: false},
		{name: "bounded_base512_cap8", base: 512 * 1024, size: 512 * 1024, capacity: 8, bounded: true},
		{name: "soft_base512_oversized", base: 512 * 1024, size: 6 * 1024 * 1024, bounded: false},
		{name: "bounded_base512_oversized", base: 512 * 1024, size: 6 * 1024 * 1024, capacity: 8, bounded: true},
		{name: "soft_base256_hit", base: 256 * 1024, size: 128 * 1024, bounded: false},
	}
	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			benchmarkBucketedBytesPoolGetPut(b, tc.base, tc.size, tc.capacity, tc.bounded)
		})
	}
}

func benchmarkBufferPoolGetPut(b *testing.B, capacity int, bounded bool) {
	b.Helper()
	size := 512 * 1024
	var opts []BufferPoolOption
	if bounded {
		opts = []BufferPoolOption{
			WithBoundedMode(true),
			WithBoundedCapacity(capacity),
		}
	}
	p := NewBufferPool(size, size, opts...)
	mon := benchreport.Start(b, int64(size))
	b.ReportAllocs()
	b.SetBytes(int64(size))
	b.ResetTimer()
	mon.MarkTimerStart()
	for i := 0; i < b.N; i++ {
		buf := p.Get()
		p.Put(buf)
		mon.SamplePeriodically(i)
	}
}

func BenchmarkBufferPool_GetPut(b *testing.B) {
	b.Run("soft_512K", func(b *testing.B) {
		benchmarkBufferPoolGetPut(b, 0, false)
	})
	b.Run("bounded_cap2", func(b *testing.B) {
		benchmarkBufferPoolGetPut(b, 2, true)
	})
}

func BenchmarkBytesPool_GetPut_Parallel(b *testing.B) {
	size := 512 * 1024
	p := NewBytesPool(size,
		WithPoolBoundedMode(true),
		WithPoolBoundedCapacity(4),
	)
	mon := benchreport.Start(b, int64(size))
	b.ReportAllocs()
	b.SetBytes(int64(size))
	b.ResetTimer()
	mon.MarkTimerStart()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			buf := p.GetBytes()
			p.PutBytes(buf)
			mon.SamplePeriodically(i)
			i++
		}
	})
}
