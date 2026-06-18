package record_strategies

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bilirec/bilirec/internal/processors"
)

func testInitSegment(brand string) []byte {
	init := make([]byte, 20)
	init[0] = 0x00
	init[1] = 0x00
	init[2] = 0x00
	init[3] = 0x14
	copy(init[4:8], "ftyp")
	copy(init[8:12], []byte(brand))
	copy(init[16:20], []byte(brand))
	return init
}

func testMediaFragment() []byte {
	moof := make([]byte, 16)
	moof[0] = 0x00
	moof[1] = 0x00
	moof[2] = 0x00
	moof[3] = 0x10
	copy(moof[4:8], "moof")
	copy(moof[8:12], "mfhd")
	moof[12] = 0x00
	moof[13] = 0x00
	moof[14] = 0x00
	moof[15] = 0x08
	return moof
}

func fileStartsWithFtyp(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if len(data) < 8 {
		return false, nil
	}
	return string(data[4:8]) == "ftyp", nil
}

func TestHlsFmp4Strategy_SkipsDuplicateInitInSingleFile(t *testing.T) {
	ensureBenchmarkConfig()
	ctx := context.Background()
	strategy := NewHlsFmp4Strategy(10000)
	outPath := filepath.Join(t.TempDir(), "single.fmp4")

	pipe, err := strategy.BuildPipeline(ctx, outPath, &RotationState{Data: map[string][]byte{}})
	if err != nil {
		t.Fatalf("BuildPipeline: %v", err)
	}
	if err := pipe.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}

	init := testInitSegment("isom")
	for _, chunk := range [][]byte{init, testMediaFragment(), init, testMediaFragment()} {
		if _, err := pipe.Process(ctx, chunk); err != nil {
			t.Fatalf("Process: %v", err)
		}
	}
	pipe.Close()

	ok, err := fileStartsWithFtyp(outPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !ok {
		t.Fatal("expected output file to start with ftyp")
	}

	st, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// init + moof + moof (duplicate init skipped)
	wantMin := int64(len(init) + len(testMediaFragment())*2)
	if st.Size() < wantMin {
		t.Fatalf("expected file size >= %d, got %d", wantMin, st.Size())
	}
}

func TestHlsFmp4Strategy_RotatesOnChangedInitWithPendingReplay(t *testing.T) {
	ensureBenchmarkConfig()
	ctx := context.Background()
	strategy := NewHlsFmp4Strategy(10000)
	dir := t.TempDir()
	seg0 := filepath.Join(dir, "seg0.fmp4")
	seg1 := filepath.Join(dir, "seg1.fmp4")

	pipe0, err := strategy.BuildPipeline(ctx, seg0, &RotationState{Data: map[string][]byte{}})
	if err != nil {
		t.Fatalf("BuildPipeline seg0: %v", err)
	}
	if err := pipe0.Open(ctx); err != nil {
		t.Fatalf("Open seg0: %v", err)
	}

	init1 := testInitSegment("isom")
	moof1 := testMediaFragment()
	if _, err := pipe0.Process(ctx, init1); err != nil {
		t.Fatalf("Process init1: %v", err)
	}
	if _, err := pipe0.Process(ctx, moof1); err != nil {
		t.Fatalf("Process moof1: %v", err)
	}

	init2 := testInitSegment("mp42")
	_, procErr := pipe0.Process(ctx, init2)
	if !errors.Is(procErr, processors.ErrFmp4Discontinuity) {
		t.Fatalf("expected discontinuity, got %v", procErr)
	}
	pipe0.Close()

	handle := strategy.HandleErr(procErr)
	if handle.Action != ErrActionRotate {
		t.Fatalf("expected rotate action, got %v", handle.Action)
	}
	if len(handle.State.Data[fmp4StatePendingInit]) == 0 {
		t.Fatal("expected pending init in rotation state")
	}

	pipe1, err := strategy.BuildPipeline(ctx, seg1, handle.State)
	if err != nil {
		t.Fatalf("BuildPipeline seg1: %v", err)
	}
	if err := pipe1.Open(ctx); err != nil {
		t.Fatalf("Open seg1: %v", err)
	}

	moof2 := testMediaFragment()
	if _, err := pipe1.Process(ctx, moof2); err != nil {
		t.Fatalf("Process moof2: %v", err)
	}
	pipe1.Close()

	ok0, err := fileStartsWithFtyp(seg0)
	if err != nil {
		t.Fatalf("Read seg0: %v", err)
	}
	if !ok0 {
		t.Fatal("seg0 should start with ftyp")
	}

	ok1, err := fileStartsWithFtyp(seg1)
	if err != nil {
		t.Fatalf("Read seg1: %v", err)
	}
	if !ok1 {
		t.Fatal("seg1 should start with ftyp after init writer replay")
	}

	data1, err := os.ReadFile(seg1)
	if err != nil {
		t.Fatalf("Read seg1 bytes: %v", err)
	}
	if len(data1) < len(init2)+len(moof2) {
		t.Fatalf("seg1 too small: %d", len(data1))
	}
	off := len(init2)
	if string(data1[off+4:off+8]) != "moof" {
		t.Fatalf("expected moof after init in seg1, got %q", string(data1[off+4:off+8]))
	}
}
