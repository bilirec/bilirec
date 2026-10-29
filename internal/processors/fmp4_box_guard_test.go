package processors

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/bilirec/bilirec/pkg/hls"
	"github.com/bilirec/bilirec/pkg/logger"
)

// makeInitSegment creates a minimal ftyp box as an init segment
func makeInitSegment() []byte {
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
	ftyp[15] = 0x00
	copy(ftyp[16:20], "isom")
	return ftyp
}

func makeDifferentInitSegment() []byte {
	ftyp := makeInitSegment()
	copy(ftyp[8:12], "mp42") // different major brand
	return ftyp
}

// makeMediaFragment creates a minimal moof box as a media fragment
func makeMediaFragment() []byte {
	moof := make([]byte, 16)
	moof[0] = 0x00
	moof[1] = 0x00
	moof[2] = 0x00
	moof[3] = 0x10 // size = 16
	copy(moof[4:8], "moof")
	copy(moof[8:12], "mfhd")
	moof[12] = 0x00
	moof[13] = 0x00
	moof[14] = 0x00
	moof[15] = 0x08
	return moof
}

// makeStypSegment creates a minimal styp box (segment type box)
func makeStypSegment() []byte {
	styp := make([]byte, 16)
	styp[0] = 0x00
	styp[1] = 0x00
	styp[2] = 0x00
	styp[3] = 0x10
	copy(styp[4:8], "styp")
	copy(styp[8:12], "isom")
	copy(styp[12:16], "isom")
	return styp
}

func newFmp4BoxGuardForTest() (*Fmp4BoxGuardProcessor, *[]byte) {
	bases := make(map[uint32]uint64)
	lastInit := make([]byte, 0)
	return &Fmp4BoxGuardProcessor{bases: &bases, lastInit: &lastInit}, &lastInit
}

func TestFmp4BoxGuard_AllowsInitSegmentFirst(t *testing.T) {
	processor, lastInit := newFmp4BoxGuardForTest()
	ctx := context.Background()
	log := logger.Nop()

	if err := processor.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	initSeg := makeInitSegment()
	result, err := processor.Process(ctx, log, initSeg)
	if err != nil {
		t.Fatalf("Process init segment failed: %v", err)
	}
	if result == nil {
		t.Fatal("Expected init segment to pass through, got nil")
	}
	if len(*lastInit) == 0 {
		t.Fatal("Expected lastInit to be stored")
	}
}

func TestFmp4BoxGuard_AllowsMediaFragmentAfterInit(t *testing.T) {
	processor, _ := newFmp4BoxGuardForTest()
	ctx := context.Background()
	log := logger.Nop()

	if err := processor.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	initSeg := makeInitSegment()
	_, _ = processor.Process(ctx, log, initSeg)

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
	processor, _ := newFmp4BoxGuardForTest()
	ctx := context.Background()
	log := logger.Nop()

	if err := processor.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	initSeg := makeInitSegment()
	_, _ = processor.Process(ctx, log, initSeg)

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
	processor, _ := newFmp4BoxGuardForTest()
	ctx := context.Background()
	log := logger.Nop()

	if err := processor.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	initSeg := makeInitSegment()
	_, _ = processor.Process(ctx, log, initSeg)

	mediaSeg := makeMediaFragment()
	_, _ = processor.Process(ctx, log, mediaSeg)

	secondInit := makeDifferentInitSegment()
	result, err := processor.Process(ctx, log, secondInit)

	if err == nil {
		t.Fatal("Expected ErrFmp4Discontinuity when init content changes after media, got nil")
	}
	if !errors.Is(err, ErrFmp4Discontinuity) {
		t.Fatalf("Expected ErrFmp4Discontinuity, got %v", err)
	}
	var disc *Fmp4DiscontinuityError
	if !errors.As(err, &disc) {
		t.Fatalf("Expected Fmp4DiscontinuityError, got %T", err)
	}
	if len(disc.InitSegment) == 0 {
		t.Fatal("Expected InitSegment in discontinuity error")
	}
	if result != nil {
		t.Fatal("Expected nil result on discontinuity, got data")
	}
}

func TestFmp4BoxGuard_SkipsDuplicateInitAfterMedia(t *testing.T) {
	processor, _ := newFmp4BoxGuardForTest()
	ctx := context.Background()
	log := logger.Nop()

	if err := processor.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	initSeg := makeInitSegment()
	_, _ = processor.Process(ctx, log, initSeg)
	_, _ = processor.Process(ctx, log, makeMediaFragment())

	result, err := processor.Process(ctx, log, initSeg)
	if err != nil {
		t.Fatalf("duplicate init should not error, got: %v", err)
	}
	if result != nil {
		t.Fatal("Expected duplicate init to be skipped (nil), got data")
	}
}

func TestFmp4BoxGuard_DropsUnknownBoxType(t *testing.T) {
	processor, _ := newFmp4BoxGuardForTest()
	ctx := context.Background()
	log := logger.Nop()

	if err := processor.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

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
	processor, _ := newFmp4BoxGuardForTest()
	ctx := context.Background()
	log := logger.Nop()

	if err := processor.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

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
	processor, _ := newFmp4BoxGuardForTest()
	ctx := context.Background()
	log := logger.Nop()

	if err := processor.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	result, err := processor.Process(ctx, log, []byte{})
	if err != nil {
		t.Fatalf("Process empty segment failed: %v", err)
	}
	if result == nil {
		t.Fatal("Expected empty segment to pass through, got nil")
	}
}

func TestFmp4BoxGuard_ResetsSeenMediaOnOpen(t *testing.T) {
	processor, _ := newFmp4BoxGuardForTest()
	processor.seenMedia = true

	ctx := context.Background()
	log := logger.Nop()

	if err := processor.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	if processor.seenMedia {
		t.Fatal("Expected seenMedia to be reset on Open")
	}
}

func TestFmp4BoxGuard_DiscontinuityScenario(t *testing.T) {
	processor, _ := newFmp4BoxGuardForTest()
	ctx := context.Background()
	log := logger.Nop()

	if err := processor.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	init1 := makeInitSegment()
	if _, err := processor.Process(ctx, log, init1); err != nil {
		t.Fatalf("First init should pass: %v", err)
	}

	if _, err := processor.Process(ctx, log, makeMediaFragment()); err != nil {
		t.Fatalf("First media should pass: %v", err)
	}
	if _, err := processor.Process(ctx, log, makeMediaFragment()); err != nil {
		t.Fatalf("Second media should pass: %v", err)
	}

	// Identical init re-send should be skipped, not rotate.
	if _, err := processor.Process(ctx, log, init1); err != nil {
		t.Fatalf("Identical init re-send should be skipped: %v", err)
	}

	// Changed init should trigger discontinuity immediately.
	init2 := makeDifferentInitSegment()
	_, err := processor.Process(ctx, log, init2)
	if !errors.Is(err, ErrFmp4Discontinuity) {
		t.Fatalf("Expected ErrFmp4Discontinuity on changed init, got %v", err)
	}
}

func TestFmp4BoxGuard_ChangedInitRotatesImmediately(t *testing.T) {
	processor, _ := newFmp4BoxGuardForTest()
	ctx := context.Background()
	log := logger.Nop()
	_ = processor.Open(ctx, log)

	_, _ = processor.Process(ctx, log, makeInitSegment())
	_, _ = processor.Process(ctx, log, makeMediaFragment())

	_, err := processor.Process(ctx, log, makeDifferentInitSegment())
	if !errors.Is(err, ErrFmp4Discontinuity) {
		t.Fatalf("expected immediate discontinuity on changed init, got %v", err)
	}
}

func TestFmp4BoxGuard_SkipsMuxCompatibleInitAfterMedia(t *testing.T) {
	initA := moovInitForGuardTest(1000)
	initB := moovInitForGuardTest(9999)
	if !hls.InitMuxCompatible(initA, initB) {
		t.Fatal("fixture inits must be mux-compatible")
	}

	processor, lastInit := newFmp4BoxGuardForTest()
	ctx := context.Background()
	log := logger.Nop()
	_ = processor.Open(ctx, log)

	_, _ = processor.Process(ctx, log, initA)
	_, _ = processor.Process(ctx, log, makeMediaFragment())

	result, err := processor.Process(ctx, log, initB)
	if err != nil {
		t.Fatalf("mux-compatible init after media should skip: %v", err)
	}
	if result != nil {
		t.Fatal("expected skip (nil result)")
	}
	if !bytes.Equal(*lastInit, initA) {
		t.Fatal("lastInit should remain first init after compatible skip")
	}
}

func moovInitForGuardTest(mvhdDuration uint32) []byte {
	ftyp := make([]byte, 20)
	binary.BigEndian.PutUint32(ftyp[0:4], 20)
	copy(ftyp[4:8], "ftyp")
	copy(ftyp[8:12], "isom")
	copy(ftyp[16:20], "isom")
	moov := moovBoxForGuardTest(mvhdDuration)
	return append(ftyp, moov...)
}

func moovBoxForGuardTest(mvhdDuration uint32) []byte {
	trak := trakBoxForGuardTest()
	mvhd := make([]byte, 108)
	binary.BigEndian.PutUint32(mvhd[0:4], 108)
	copy(mvhd[4:8], "mvhd")
	binary.BigEndian.PutUint32(mvhd[20:24], mvhdDuration)
	binary.BigEndian.PutUint32(mvhd[24:28], 1000)
	children := append(mvhd, trak...)
	moov := make([]byte, 8+len(children))
	binary.BigEndian.PutUint32(moov[0:4], uint32(len(moov)))
	copy(moov[4:8], "moov")
	copy(moov[8:], children)
	return moov
}

func trakBoxForGuardTest() []byte {
	tkhd := make([]byte, 92)
	binary.BigEndian.PutUint32(tkhd[0:4], 92)
	copy(tkhd[4:8], "tkhd")
	binary.BigEndian.PutUint32(tkhd[12:16], 0x01020304)
	binary.BigEndian.PutUint32(tkhd[20:24], 1)
	binary.BigEndian.PutUint32(tkhd[84:88], 1280<<16)
	binary.BigEndian.PutUint32(tkhd[88:92], 720<<16)
	mdhd := make([]byte, 32)
	binary.BigEndian.PutUint32(mdhd[0:4], 32)
	copy(mdhd[4:8], "mdhd")
	binary.BigEndian.PutUint32(mdhd[20:24], 90000)
	hdlr := make([]byte, 44)
	binary.BigEndian.PutUint32(hdlr[0:4], 44)
	copy(hdlr[4:8], "hdlr")
	copy(hdlr[16:20], "vide")
	avcC := wrapGuardBox("avcC", []byte{1, 2})
	avc1 := make([]byte, 86+len(avcC))
	binary.BigEndian.PutUint32(avc1[0:4], uint32(len(avc1)))
	copy(avc1[4:8], "avc1")
	copy(avc1[86:], avcC)
	stsdPayload := make([]byte, 8+len(avc1))
	binary.BigEndian.PutUint32(stsdPayload[4:8], 1)
	copy(stsdPayload[8:], avc1)
	stsd := wrapGuardBox("stsd", stsdPayload)
	stbl := wrapGuardBox("stbl", stsd)
	minf := wrapGuardBox("minf", stbl)
	mdiaPayload := append(append(mdhd, hdlr...), minf...)
	mdia := wrapGuardBox("mdia", mdiaPayload)
	trakPayload := append(tkhd, mdia...)
	trak := make([]byte, 8+len(trakPayload))
	binary.BigEndian.PutUint32(trak[0:4], uint32(len(trak)))
	copy(trak[4:8], "trak")
	copy(trak[8:], trakPayload)
	return trak
}

func wrapGuardBox(typ string, payload []byte) []byte {
	out := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(out[0:4], uint32(len(out)))
	copy(out[4:8], []byte(typ))
	copy(out[8:], payload)
	return out
}
