package pool

import "testing"

func BenchmarkBucketedBytesPool_GetPut(b *testing.B) {
	cases := []struct {
		name string
		base int
		size int
	}{
		{name: "base512_hit", base: 512 * 1024, size: 256 * 1024},
		{name: "base512_edge", base: 512 * 1024, size: 512 * 1024},
		{name: "base512_oversized", base: 512 * 1024, size: 6 * 1024 * 1024},
		{name: "base256_hit", base: 256 * 1024, size: 128 * 1024},
	}
	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			p := NewBucketedBytesPool(tc.base)
			b.ReportAllocs()
			b.SetBytes(int64(tc.size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf := p.GetSized(tc.size)
				p.Put(buf)
			}
		})
	}
}
