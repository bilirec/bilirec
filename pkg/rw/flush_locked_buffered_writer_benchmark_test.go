package rw

import (
	"io"
	"sync"
	"testing"
)

type benchNoopWriter struct{}

func (benchNoopWriter) Write(p []byte) (int, error) { return len(p), nil }

func BenchmarkFlushLockedBufferedWriter_WriteAndFlush(b *testing.B) {
	cases := []struct {
		name      string
		bufSize   int
		writeSize int
	}{
		{name: "small_write", bufSize: 4 * 1024, writeSize: 512},
		{name: "equal_write", bufSize: 4 * 1024, writeSize: 4 * 1024},
		{name: "large_write", bufSize: 4 * 1024, writeSize: 16 * 1024},
	}

	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			writer := NewFlushLockedBufferedWriter(benchNoopWriter{}, tc.bufSize, &sync.Mutex{})
			data := make([]byte, tc.writeSize)
			b.ReportAllocs()
			b.SetBytes(int64(tc.writeSize))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := writer.Write(data); err != nil {
					b.Fatalf("write failed: %v", err)
				}
				if err := writer.Flush(); err != nil {
					b.Fatalf("flush failed: %v", err)
				}
			}
		})
	}
}

func BenchmarkFlushLockedBufferedWriter_ParallelSharedLock(b *testing.B) {
	var mu sync.Mutex
	writer := NewFlushLockedBufferedWriter(io.Discard, 8*1024, &mu)
	data := make([]byte, 1024)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := writer.Write(data); err != nil {
				b.Fatalf("write failed: %v", err)
			}
			if err := writer.Flush(); err != nil {
				b.Fatalf("flush failed: %v", err)
			}
		}
	})
}
