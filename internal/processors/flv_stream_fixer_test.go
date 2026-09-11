package processors_test

import (
	"context"
	"testing"

	"github.com/bilirec/bilirec/internal/processors"
	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/pipeline"
)

func TestFlvStreamFixer_OpenPreservesJumpReporter(t *testing.T) {
	fixer := flv.NewRealtimeFixer()
	defer fixer.Close()

	var reporterCalls int
	fixer.SetTimestampJumpReporter(func(w flv.TimestampJumpWarning) {
		reporterCalls++
	})

	pipe := pipeline.New(processors.NewFlvStreamFixerWithFixer(fixer))
	ctx := context.Background()
	if err := pipe.Open(ctx); err != nil {
		t.Fatalf("open: %v", err)
	}

	tag1 := flv.NewTagBytes(flv.TagTypeAudio, []byte{0xaf, 0x01, 0x11})
	setTagTimestamp(tag1, 0)
	tag2 := flv.NewTagBytes(flv.TagTypeAudio, []byte{0xaf, 0x01, 0x22})
	setTagTimestamp(tag2, 1200)

	in := make([]byte, 0, flv.PrevTagSizeBytes+len(tag1)+len(tag2))
	in = append(in, 0, 0, 0, 0)
	in = append(in, tag1...)
	in = append(in, tag2...)

	if _, err := fixer.Fix(flv.FlvHeader); err != nil {
		t.Fatalf("header: %v", err)
	}
	if _, err := pipe.Process(ctx, in); err != nil {
		t.Fatalf("process: %v", err)
	}
	if reporterCalls != 1 {
		t.Fatalf("expected reporter to survive Open, got %d calls", reporterCalls)
	}

	// Re-open simulates segment rotation; reporter must still work.
	if err := pipe.Open(ctx); err != nil {
		t.Fatalf("re-open: %v", err)
	}
	reporterCalls = 0
	if _, err := pipe.Process(ctx, in); err != nil {
		t.Fatalf("process after re-open: %v", err)
	}
	if reporterCalls != 1 {
		t.Fatalf("expected reporter after re-open, got %d calls", reporterCalls)
	}
}

func setTagTimestamp(tag []byte, timestamp uint32) {
	tag[4] = byte(timestamp >> 16)
	tag[5] = byte(timestamp >> 8)
	tag[6] = byte(timestamp)
	tag[7] = byte(timestamp >> 24)
}
