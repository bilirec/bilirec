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

	const timescale = 1000
	last := map[uint32]uint64{1: 0}
	moof := buildNormalizerTestMoof(1, 11000)
	proc.timescale = timescale
	proc.lastRawTfdt = last

	if _, err := proc.Process(ctx, log, moof); err != nil {
		t.Fatal(err)
	}
	if reported < 11*time.Second {
		t.Fatalf("expected gap callback, got %v", reported)
	}
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
