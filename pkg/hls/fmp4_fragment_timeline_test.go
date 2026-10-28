package hls

import (
	"encoding/binary"
	"testing"
	"time"
)

func TestNoteFragmentTimelineForwardGap(t *testing.T) {
	const timescale = 1000
	last := map[uint32]uint64{1: 0}
	// +11s at timescale 1000 => delta ticks 11000
	moof := buildTestMoof(1, 11000, 0)
	gap := NoteFragmentTimeline(moof, timescale, last)
	if gap < 11*time.Second {
		t.Fatalf("expected forward gap >= 11s, got %v", gap)
	}
}

func TestNoteFragmentTimelineNormalStep(t *testing.T) {
	const timescale = 1000
	last := map[uint32]uint64{1: 0}
	moof := buildTestMoof(1, 2000, 0)
	if gap := NoteFragmentTimeline(moof, timescale, last); gap != 0 {
		t.Fatalf("expected no gap for 2s step, got %v", gap)
	}
}

func buildTestMoof(trackID uint32, tfdtV0 uint32, version byte) []byte {
	tfhd := make([]byte, 16)
	binary.BigEndian.PutUint32(tfhd[0:4], 16)
	binary.BigEndian.PutUint32(tfhd[4:8], boxTypeTfhd)
	binary.BigEndian.PutUint32(tfhd[12:16], trackID)

	tfdt := make([]byte, 16)
	binary.BigEndian.PutUint32(tfdt[0:4], 16)
	binary.BigEndian.PutUint32(tfdt[4:8], boxTypeTfdt)
	tfdt[8] = version
	binary.BigEndian.PutUint32(tfdt[12:16], tfdtV0)

	trafPayload := append(tfhd, tfdt...)
	traf := make([]byte, 8+len(trafPayload))
	binary.BigEndian.PutUint32(traf[0:4], uint32(8+len(trafPayload)))
	binary.BigEndian.PutUint32(traf[4:8], boxTypeTraf)
	copy(traf[8:], trafPayload)

	moofPayload := traf
	moof := make([]byte, 8+len(moofPayload))
	binary.BigEndian.PutUint32(moof[0:4], uint32(8+len(moofPayload)))
	binary.BigEndian.PutUint32(moof[4:8], boxTypeMoof)
	copy(moof[8:], moofPayload)
	return moof
}
