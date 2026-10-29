package hls

import (
	"encoding/binary"
	"testing"
	"time"
)

func TestNoteFragmentTimelineForwardGap(t *testing.T) {
	last := map[uint32]uint64{1: 0}
	// +11s at timescale 1000 => delta ticks 11000
	moof := buildTestMoof(1, 11000, 0)
	gap := NoteFragmentTimeline(moof, map[uint32]uint32{1: 1000}, last)
	if gap < 11*time.Second {
		t.Fatalf("expected forward gap >= 11s, got %v", gap)
	}
}

func TestNoteFragmentTimelineUsesPerTrackTimescale(t *testing.T) {
	// 11s of video at 12800. Dividing by the audio timescale (44100) yields ~3.2s,
	// which is under the forward threshold and would report no gap.
	last := map[uint32]uint64{1: 0}
	moof := buildTestMoof(1, 11*12800, 0)
	gap := NoteFragmentTimeline(moof, map[uint32]uint32{1: 12800, 2: 44100}, last)
	if gap < 11*time.Second || gap >= 12*time.Second {
		t.Fatalf("video gap = %v, want about 11s", gap)
	}
}

func TestTrackTimescalesFromInit(t *testing.T) {
	init := buildTwoTrackInit(1, 12800, 2, 44100)
	got := TrackTimescalesFromInit(init)
	if got[1] != 12800 || got[2] != 44100 || len(got) != 2 {
		t.Fatalf("timescales = %#v", got)
	}
}

func TestTrackTimescalesFromInit_NoMoov(t *testing.T) {
	moof := buildTestMoof(1, 1000, 0)
	if got := TrackTimescalesFromInit(moof); got != nil {
		t.Fatalf("expected nil without moov, got %#v", got)
	}
}

func TestSegmentHasMoov(t *testing.T) {
	init := buildTwoTrackInit(1, 12800, 2, 44100)
	if !SegmentHasMoov(init) {
		t.Fatal("init should contain moov")
	}
	if SegmentHasMoov(buildTestMoof(1, 1000, 0)) {
		t.Fatal("moof-only should not report moov")
	}
}

func TestNoteFragmentTimelineNormalStep(t *testing.T) {
	last := map[uint32]uint64{1: 0}
	moof := buildTestMoof(1, 2000, 0)
	if gap := NoteFragmentTimeline(moof, map[uint32]uint32{1: 1000}, last); gap != 0 {
		t.Fatalf("expected no gap for 2s step, got %v", gap)
	}
}

func buildTwoTrackInit(videoID, videoTS, audioID, audioTS uint32) []byte {
	trakV := buildTimelineTrak(videoID, videoTS)
	trakA := buildTimelineTrak(audioID, audioTS)
	children := append(trakV, trakA...)
	moov := make([]byte, 8+len(children))
	binary.BigEndian.PutUint32(moov[0:4], uint32(len(moov)))
	binary.BigEndian.PutUint32(moov[4:8], boxTypeMoov)
	copy(moov[8:], children)
	return moov
}

func buildTimelineTrak(trackID, timescale uint32) []byte {
	tkhd := make([]byte, 92)
	binary.BigEndian.PutUint32(tkhd[0:4], 92)
	binary.BigEndian.PutUint32(tkhd[4:8], boxTypeTkhd)
	binary.BigEndian.PutUint32(tkhd[12:16], 0x01020304)
	binary.BigEndian.PutUint32(tkhd[20:24], trackID)
	binary.BigEndian.PutUint32(tkhd[84:88], 320<<16)
	binary.BigEndian.PutUint32(tkhd[88:92], 240<<16)

	mdhd := make([]byte, 32)
	binary.BigEndian.PutUint32(mdhd[0:4], 32)
	binary.BigEndian.PutUint32(mdhd[4:8], boxTypeMdhd)
	binary.BigEndian.PutUint32(mdhd[20:24], timescale)

	mdia := make([]byte, 8+len(mdhd))
	binary.BigEndian.PutUint32(mdia[0:4], uint32(len(mdia)))
	binary.BigEndian.PutUint32(mdia[4:8], boxTypeMdia)
	copy(mdia[8:], mdhd)

	children := append(tkhd, mdia...)
	trak := make([]byte, 8+len(children))
	binary.BigEndian.PutUint32(trak[0:4], uint32(len(trak)))
	binary.BigEndian.PutUint32(trak[4:8], boxTypeTrak)
	copy(trak[8:], children)
	return trak
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
