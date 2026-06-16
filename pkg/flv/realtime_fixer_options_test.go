package flv_test

import (
	"testing"

	"github.com/bilirec/bilirec/pkg/flv"
)

func TestRealtimeFixer_WithBufferSizes(t *testing.T) {
	const chunkSize = 256 * 1024
	chunk := generateFLVChunk(chunkSize)
	header := []byte{'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09}

	fixer := flv.NewRealtimeFixer(flv.WithBufferSizes(chunkSize, chunkSize))
	defer fixer.Close()

	if _, err := fixer.Fix(header); err != nil {
		t.Fatalf("header fix: %v", err)
	}
	out, err := fixer.Fix(chunk)
	if err != nil {
		t.Fatalf("fix chunk: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected non-empty output")
	}

	defaultFixer := flv.NewRealtimeFixer()
	defer defaultFixer.Close()
	if _, err := defaultFixer.Fix(header); err != nil {
		t.Fatalf("default header fix: %v", err)
	}
	defaultOut, err := defaultFixer.Fix(chunk)
	if err != nil {
		t.Fatalf("default fix chunk: %v", err)
	}
	if len(defaultOut) == 0 {
		t.Fatal("expected non-empty default output")
	}
}

func TestRealtimeFixer_WithBufferSizes_InvalidValuesUseDefaults(t *testing.T) {
	fixer := flv.NewRealtimeFixer(flv.WithBufferSizes(0, 0))
	defer fixer.Close()

	header := []byte{'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09}
	if _, err := fixer.Fix(header); err != nil {
		t.Fatalf("fix header: %v", err)
	}
}
