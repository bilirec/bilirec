package processors

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"
)

func TestFmp4TimestampNormalizer_OnGap(t *testing.T) {
	bases := map[uint32]uint64{}
	var reported time.Duration
	proc := &Fmp4TimestampNormalizerProcessor{
		bases: &bases,
		onGap: func(g TimelineGap) { reported = g.Delta },
	}

	ctx := context.Background()
	log := logger.Named("test")
	if err := proc.Open(ctx, log); err != nil {
		t.Fatal(err)
	}

	last := map[uint32]uint64{1: 0}
	moof := buildNormalizerTestMoof(1, 11000)
	proc.timescales = map[uint32]uint32{1: 1000}
	proc.lastRawTfdt = last

	if _, err := proc.Process(ctx, log, moof); err != nil {
		t.Fatal(err)
	}
	if reported < 11*time.Second {
		t.Fatalf("expected gap callback, got %v", reported)
	}
}

// The normalizer must learn timescales from the init segment and keep them
// across media segments without rescanning each fragment.
func TestFmp4TimestampNormalizer_CachesTimescalesFromInit(t *testing.T) {
	bases := map[uint32]uint64{}
	var reported time.Duration
	proc := &Fmp4TimestampNormalizerProcessor{
		bases: &bases,
		onGap: func(g TimelineGap) { reported = g.Delta },
	}
	ctx := context.Background()
	log := logger.Named("test")
	if err := proc.Open(ctx, log); err != nil {
		t.Fatal(err)
	}

	init := buildNormalizerTestInit(1, 1000)
	if _, err := proc.Process(ctx, log, init); err != nil {
		t.Fatal(err)
	}
	if proc.timescales[1] != 1000 {
		t.Fatalf("timescales not learned from init: %#v", proc.timescales)
	}

	// Media fragments carry no moov; the cached map must survive and still
	// produce an 11s gap at timescale 1000.
	if _, err := proc.Process(ctx, log, buildNormalizerTestMoof(1, 1000)); err != nil {
		t.Fatal(err)
	}
	if reported != 0 {
		t.Fatalf("first fragment must not report a gap, got %v", reported)
	}
	if _, err := proc.Process(ctx, log, buildNormalizerTestMoof(1, 12000)); err != nil {
		t.Fatal(err)
	}
	if reported < 11*time.Second {
		t.Fatalf("expected gap from cached timescales, got %v", reported)
	}
	if proc.timescales[1] != 1000 {
		t.Fatalf("cached timescales were lost: %#v", proc.timescales)
	}
}

// A mid-stream init change must replace the cached timescales rather than
// keeping the stale ones forever.
func TestFmp4TimestampNormalizer_RefreshesTimescalesOnNewInit(t *testing.T) {
	bases := map[uint32]uint64{}
	proc := &Fmp4TimestampNormalizerProcessor{bases: &bases}
	ctx := context.Background()
	log := logger.Named("test")
	if err := proc.Open(ctx, log); err != nil {
		t.Fatal(err)
	}

	if _, err := proc.Process(ctx, log, buildNormalizerTestInit(1, 12800)); err != nil {
		t.Fatal(err)
	}
	if proc.timescales[1] != 12800 {
		t.Fatalf("initial timescale = %d, want 12800", proc.timescales[1])
	}

	// Quality switch: same track id, different timescale.
	if _, err := proc.Process(ctx, log, buildNormalizerTestInit(1, 90000)); err != nil {
		t.Fatal(err)
	}
	if proc.timescales[1] != 90000 {
		t.Fatalf("timescale not refreshed: %d, want 90000", proc.timescales[1])
	}
}

// Media-only input before any init must not panic and must not report gaps.
func TestFmp4TimestampNormalizer_NoInitNoGap(t *testing.T) {
	bases := map[uint32]uint64{}
	var reported time.Duration
	proc := &Fmp4TimestampNormalizerProcessor{
		bases: &bases,
		onGap: func(g TimelineGap) { reported = g.Delta },
	}
	ctx := context.Background()
	log := logger.Named("test")
	if err := proc.Open(ctx, log); err != nil {
		t.Fatal(err)
	}

	if _, err := proc.Process(ctx, log, buildNormalizerTestMoof(1, 50000)); err != nil {
		t.Fatal(err)
	}
	if reported != 0 {
		t.Fatalf("expected no gap without timescales, got %v", reported)
	}
}

// buildNormalizerTestInit builds ftyp+moov with a single video trak whose mdhd
// timescale is timescale. Offsets follow ISO BMFF so it also exercises the
// real tkhd field positions.
func buildNormalizerTestInit(trackID, timescale uint32) []byte {
	const boxTypeFtyp = 0x66747970
	const boxTypeTrak = 0x7472616b
	const boxTypeTkhd = 0x746b6864
	const boxTypeMdia = 0x6d646961
	const boxTypeMdhd = 0x6d646864
	const boxTypeHdlr = 0x68646c72

	ftyp := make([]byte, 16)
	binary.BigEndian.PutUint32(ftyp[0:4], 16)
	binary.BigEndian.PutUint32(ftyp[4:8], boxTypeFtyp)
	copy(ftyp[8:12], "isom")
	copy(ftyp[12:16], "isom")

	tkhd := make([]byte, 92)
	binary.BigEndian.PutUint32(tkhd[0:4], 92)
	binary.BigEndian.PutUint32(tkhd[4:8], boxTypeTkhd)
	binary.BigEndian.PutUint32(tkhd[12:16], 0x01020304) // creation_time decoy
	binary.BigEndian.PutUint32(tkhd[20:24], trackID)
	binary.BigEndian.PutUint32(tkhd[84:88], 320<<16)
	binary.BigEndian.PutUint32(tkhd[88:92], 240<<16)

	mdhd := make([]byte, 32)
	binary.BigEndian.PutUint32(mdhd[0:4], 32)
	binary.BigEndian.PutUint32(mdhd[4:8], boxTypeMdhd)
	binary.BigEndian.PutUint32(mdhd[20:24], timescale)

	hdlr := make([]byte, 44)
	binary.BigEndian.PutUint32(hdlr[0:4], 44)
	binary.BigEndian.PutUint32(hdlr[4:8], boxTypeHdlr)
	copy(hdlr[16:20], "vide")

	mdiaPayload := append(append([]byte{}, mdhd...), hdlr...)
	mdia := make([]byte, 8+len(mdiaPayload))
	binary.BigEndian.PutUint32(mdia[0:4], uint32(len(mdia)))
	binary.BigEndian.PutUint32(mdia[4:8], boxTypeMdia)
	copy(mdia[8:], mdiaPayload)

	trakPayload := append(append([]byte{}, tkhd...), mdia...)
	trak := make([]byte, 8+len(trakPayload))
	binary.BigEndian.PutUint32(trak[0:4], uint32(len(trak)))
	binary.BigEndian.PutUint32(trak[4:8], boxTypeTrak)
	copy(trak[8:], trakPayload)

	const boxTypeMoov = 0x6d6f6f76
	moov := make([]byte, 8+len(trak))
	binary.BigEndian.PutUint32(moov[0:4], uint32(len(moov)))
	binary.BigEndian.PutUint32(moov[4:8], boxTypeMoov)
	copy(moov[8:], trak)

	return append(ftyp, moov...)
}

func buildNormalizerTestMoof(trackID uint32, tfdtV0 uint32) []byte {
	const boxTypeMoof = 0x6d6f6f66
	const boxTypeTraf = 0x74726166
	const boxTypeTfhd = 0x74666864
	const boxTypeTfdt = 0x74666474

	tfhd := make([]byte, 16)
	binary.BigEndian.PutUint32(tfhd[0:4], 16)
	binary.BigEndian.PutUint32(tfhd[4:8], boxTypeTfhd)
	binary.BigEndian.PutUint32(tfhd[12:16], trackID)

	tfdt := make([]byte, 16)
	binary.BigEndian.PutUint32(tfdt[0:4], 16)
	binary.BigEndian.PutUint32(tfdt[4:8], boxTypeTfdt)
	binary.BigEndian.PutUint32(tfdt[12:16], tfdtV0)

	trafPayload := append(tfhd, tfdt...)
	traf := make([]byte, 8+len(trafPayload))
	binary.BigEndian.PutUint32(traf[0:4], uint32(8+len(trafPayload)))
	binary.BigEndian.PutUint32(traf[4:8], boxTypeTraf)
	copy(traf[8:], trafPayload)

	moof := make([]byte, 8+len(traf))
	binary.BigEndian.PutUint32(moof[0:4], uint32(8+len(traf)))
	binary.BigEndian.PutUint32(moof[4:8], boxTypeMoof)
	copy(moof[8:], traf)
	return moof
}
