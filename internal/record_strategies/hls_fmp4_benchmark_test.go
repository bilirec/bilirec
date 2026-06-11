package record_strategies

import (
	"context"
	"encoding/binary"
	"path/filepath"
	"testing"
)

func boxBytes(typ string, payload []byte) []byte {
	size := 8 + len(payload)
	out := make([]byte, size)
	binary.BigEndian.PutUint32(out[:4], uint32(size))
	copy(out[4:8], []byte(typ))
	copy(out[8:], payload)
	return out
}

func makeFmp4Fragment(trackID uint32, decodeTime uint64) []byte {
	tfhdPayload := make([]byte, 4)
	binary.BigEndian.PutUint32(tfhdPayload, trackID)
	tfhd := boxBytes("tfhd", append([]byte{0, 0, 0, 0}, tfhdPayload...))

	tfdtPayload := make([]byte, 8)
	binary.BigEndian.PutUint64(tfdtPayload, decodeTime)
	tfdt := boxBytes("tfdt", append([]byte{1, 0, 0, 0}, tfdtPayload...))

	traf := boxBytes("traf", append(tfhd, tfdt...))
	moof := boxBytes("moof", traf)
	mdat := boxBytes("mdat", make([]byte, 256*1024))
	return append(moof, mdat...)
}

func BenchmarkHlsFmp4Strategy_PipelineThroughput(b *testing.B) {
	ctx := context.Background()
	strategy := NewHlsFmp4Strategy()
	outputPath := filepath.Join(b.TempDir(), "bench.fmp4")
	pipe, err := strategy.BuildPipeline(ctx, outputPath, &RotationState{Data: map[string][]byte{}})
	if err != nil {
		b.Fatalf("BuildPipeline failed: %v", err)
	}
	if err := pipe.Open(ctx); err != nil {
		b.Fatalf("Open failed: %v", err)
	}
	defer pipe.Close()

	fragment := makeFmp4Fragment(1, 90000)
	b.ReportAllocs()
	b.SetBytes(int64(len(fragment)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := pipe.Process(ctx, fragment); err != nil {
			b.Fatalf("Process failed: %v", err)
		}
	}
}
