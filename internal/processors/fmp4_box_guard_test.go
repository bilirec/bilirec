package processors

import (
	"context"
	"errors"
	"testing"

	"github.com/sirupsen/logrus"
)

// makeInitSegment creates a minimal ftyp box as an init segment
func makeInitSegment() []byte {
	// ftyp box: size (4 bytes) + type (4 bytes) + brand + version
	ftyp := make([]byte, 20)
	ftyp[0] = 0x00
	ftyp[1] = 0x00
	ftyp[2] = 0x00
	ftyp[3] = 0x14 // size = 20
	copy(ftyp[4:8], "ftyp")
	copy(ftyp[8:12], "isom") // major brand
	ftyp[12] = 0x00
	ftyp[13] = 0x00
	ftyp[14] = 0x00
	ftyp[15] = 0x00           // minor version
	copy(ftyp[16:20], "isom") // compatible brand
	return ftyp
}

// makeMediaFragment creates a minimal moof box as a media fragment
func makeMediaFragment() []byte {
	// moof box: size (4 bytes) + type (4 bytes) + minimal content
	moof := make([]byte, 16)
	moof[0] = 0x00
	moof[1] = 0x00
	moof[2] = 0x00
	moof[3] = 0x10 // size = 16
	copy(moof[4:8], "moof")
	// Minimal mfhd + traf content (just padding for this test)
	copy(moof[8:12], "mfhd")
	moof[12] = 0x00
	moof[13] = 0x00
	moof[14] = 0x00
	moof[15] = 0x08 // mfhd size
	return moof
}

// makeStypSegment creates a minimal styp box (segment type box)
func makeStypSegment() []byte {
	styp := make([]byte, 16)
	styp[0] = 0x00
	styp[1] = 0x00
	styp[2] = 0x00
	styp[3] = 0x10 // size = 16
	copy(styp[4:8], "styp")
	copy(styp[8:12], "isom")
	copy(styp[12:16], "isom")
	return styp
}

func TestFmp4BoxGuard_AllowsInitSegmentFirst(t *testing.T) {
	bases := make(map[uint32]uint64)
	processor := &Fmp4BoxGuardProcessor{bases: &bases}
	ctx := context.Background()
	log := logrus.NewEntry(logrus.New())

	if err := processor.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// First segment: init (ftyp) should be allowed
	initSeg := makeInitSegment()
	result, err := processor.Process(ctx, log, initSeg)
	if err != nil {
		t.Fatalf("Process init segment failed: %v", err)
	}
	if result == nil {
		t.Fatal("Expected init segment to pass through, got nil")
	}
}

func TestFmp4BoxGuard_AllowsMediaFragmentAfterInit(t *testing.T) {
	bases := make(map[uint32]uint64)
	processor := &Fmp4BoxGuardProcessor{bases: &bases}
	ctx := context.Background()
	log := logrus.NewEntry(logrus.New())

	if err := processor.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// First: init segment
	initSeg := makeInitSegment()
	_, _ = processor.Process(ctx, log, initSeg)

	// Then: media fragment should be allowed
	mediaSeg := makeMediaFragment()
	result, err := processor.Process(ctx, log, mediaSeg)
	if err != nil {
		t.Fatalf("Process media fragment failed: %v", err)
	}
	if result == nil {
		t.Fatal("Expected media fragment to pass through, got nil")
	}
}

func TestFmp4BoxGuard_AllowsStypAfterInit(t *testing.T) {
	bases := make(map[uint32]uint64)
	processor := &Fmp4BoxGuardProcessor{bases: &bases}
	ctx := context.Background()
	log := logrus.NewEntry(logrus.New())

	if err := processor.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// First: init segment
	initSeg := makeInitSegment()
	_, _ = processor.Process(ctx, log, initSeg)

	// Then: styp should be allowed (not treated as discontinuity)
	stypSeg := makeStypSegment()
	result, err := processor.Process(ctx, log, stypSeg)
	if err != nil {
		t.Fatalf("Process styp segment failed: %v", err)
	}
	if result == nil {
		t.Fatal("Expected styp segment to pass through, got nil")
	}
}

func TestFmp4BoxGuard_ReturnsErrorOnDiscontinuity(t *testing.T) {
	bases := make(map[uint32]uint64)
	processor := &Fmp4BoxGuardProcessor{bases: &bases}
	ctx := context.Background()
	log := logrus.NewEntry(logrus.New())

	if err := processor.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// First: init segment
	initSeg := makeInitSegment()
	_, _ = processor.Process(ctx, log, initSeg)

	// Second: media fragment (marks seenMedia = true)
	mediaSeg := makeMediaFragment()
	_, _ = processor.Process(ctx, log, mediaSeg)

	// Third: another init segment should trigger discontinuity error
	secondInit := makeInitSegment()
	result, err := processor.Process(ctx, log, secondInit)

	// Should return error, not nil
	if err == nil {
		t.Fatal("Expected ErrFmp4Discontinuity when init segment appears after media, got nil")
	}
	if !errors.Is(err, ErrFmp4Discontinuity) {
		t.Fatalf("Expected ErrFmp4Discontinuity, got %v", err)
	}
	if result != nil {
		t.Fatal("Expected nil result on discontinuity, got data")
	}
}

func TestFmp4BoxGuard_DropsUnknownBoxType(t *testing.T) {
	bases := make(map[uint32]uint64)
	processor := &Fmp4BoxGuardProcessor{bases: &bases}
	ctx := context.Background()
	log := logrus.NewEntry(logrus.New())

	if err := processor.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Unknown box type (e.g., "abcd")
	unknown := make([]byte, 16)
	unknown[0] = 0x00
	unknown[1] = 0x00
	unknown[2] = 0x00
	unknown[3] = 0x10
	copy(unknown[4:8], "abcd")

	result, err := processor.Process(ctx, log, unknown)
	if err != nil {
		t.Fatalf("Process should not error for unknown box, got: %v", err)
	}
	if result != nil {
		t.Fatal("Expected unknown box to be dropped (nil), got data")
	}
}

func TestFmp4BoxGuard_DropsTruncatedSegment(t *testing.T) {
	bases := make(map[uint32]uint64)
	processor := &Fmp4BoxGuardProcessor{bases: &bases}
	ctx := context.Background()
	log := logrus.NewEntry(logrus.New())

	if err := processor.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Segment too short (< 8 bytes)
	short := make([]byte, 5)
	result, err := processor.Process(ctx, log, short)
	if err != nil {
		t.Fatalf("Process should not error for short segment, got: %v", err)
	}
	if result != nil {
		t.Fatal("Expected short segment to be dropped (nil), got data")
	}
}

func TestFmp4BoxGuard_AllowsEmptySegment(t *testing.T) {
	bases := make(map[uint32]uint64)
	processor := &Fmp4BoxGuardProcessor{bases: &bases}
	ctx := context.Background()
	log := logrus.NewEntry(logrus.New())

	if err := processor.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Empty segment should pass through
	result, err := processor.Process(ctx, log, []byte{})
	if err != nil {
		t.Fatalf("Process empty segment failed: %v", err)
	}
	if result == nil {
		t.Fatal("Expected empty segment to pass through, got nil")
	}
}

func TestFmp4BoxGuard_ResetsSeenMediaOnOpen(t *testing.T) {
	bases := make(map[uint32]uint64)
	processor := &Fmp4BoxGuardProcessor{bases: &bases}

	// Simulate previous state
	processor.seenMedia = true

	ctx := context.Background()
	log := logrus.NewEntry(logrus.New())

	// Open should reset seenMedia
	if err := processor.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	if processor.seenMedia {
		t.Fatal("Expected seenMedia to be reset on Open")
	}
}

// TestFmp4BoxGuard_DiscontinuityScenario 模擬真實流不連續場景
// 這個測試驗證當上游服務器重啟或切換時，init segment 出現在 media fragment 之後
// 的情況會被正確檢測並觸發文件切換
func TestFmp4BoxGuard_DiscontinuityScenario(t *testing.T) {
	bases := make(map[uint32]uint64)
	processor := &Fmp4BoxGuardProcessor{bases: &bases}
	ctx := context.Background()
	log := logrus.NewEntry(logrus.New())

	if err := processor.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// 正常流開始：init -> media1 -> media2
	init1 := makeInitSegment()
	_, err := processor.Process(ctx, log, init1)
	if err != nil {
		t.Fatalf("First init should pass: %v", err)
	}

	media1 := makeMediaFragment()
	_, err = processor.Process(ctx, log, media1)
	if err != nil {
		t.Fatalf("First media should pass: %v", err)
	}

	media2 := makeMediaFragment()
	_, err = processor.Process(ctx, log, media2)
	if err != nil {
		t.Fatalf("Second media should pass: %v", err)
	}

	// 流不連續：新的 init segment 出現（上游切換或重啟）
	// 這應該觸發 ErrFmp4Discontinuity
	init2 := makeInitSegment()
	_, err = processor.Process(ctx, log, init2)

	if !errors.Is(err, ErrFmp4Discontinuity) {
		t.Fatalf("Expected ErrFmp4Discontinuity on stream discontinuity, got %v", err)
	}

	// 在真實場景中，這個錯誤會觸發 recorder 的文件切換
	// 新的文件會以 init2 作為第一個 segment 開始
}
