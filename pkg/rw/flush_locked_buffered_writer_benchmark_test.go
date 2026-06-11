package rw

import (
	"io"
	"sync"
	"testing"

	"github.com/bilirec/bilirec/pkg/benchreport"
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
			mon := benchreport.Start(b, int64(tc.writeSize))
			b.ReportAllocs()
			b.SetBytes(int64(tc.writeSize))
			b.ResetTimer()
			mon.MarkTimerStart()
			for i := 0; i < b.N; i++ {
				if _, err := writer.Write(data); err != nil {
					b.Fatalf("write failed: %v", err)
				}
				if err := writer.Flush(); err != nil {
					b.Fatalf("flush failed: %v", err)
				}
				mon.SamplePeriodically(i)
			}
		})
	}
}

func BenchmarkFlushLockedBufferedWriter_ParallelSharedLock(b *testing.B) {
	var mu sync.Mutex
	data := make([]byte, 1024)
	mon := benchreport.Start(b, int64(len(data)))
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	mon.MarkTimerStart()
	b.RunParallel(func(pb *testing.PB) {
		// Each worker owns its writer; only the flush lock is shared (matches sequentialWrite).
		writer := NewFlushLockedBufferedWriter(io.Discard, 8*1024, &mu)
		for pb.Next() {
			if _, err := writer.Write(data); err != nil {
				b.Fatalf("write failed: %v", err)
			}
			if err := writer.Flush(); err != nil {
				b.Fatalf("flush failed: %v", err)
			}
			mon.SampleNow()
		}
	})
}
