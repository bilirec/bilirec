package hls

import (
	"encoding/binary"
	"testing"
)

func box(typ string, payload []byte) []byte {
	size := 8 + len(payload)
	out := make([]byte, size)
	binary.BigEndian.PutUint32(out[:4], uint32(size))
	copy(out[4:8], []byte(typ))
	copy(out[8:], payload)
	return out
}

func fullBox(version byte, flags uint32, payload []byte) []byte {
	out := make([]byte, 4+len(payload))
	out[0] = version
	out[1] = byte(flags >> 16)
	out[2] = byte(flags >> 8)
	out[3] = byte(flags)
	copy(out[4:], payload)
	return out
}

func makeMoof(trackID uint32, tfdtVersion byte, decodeTime uint64) []byte {
	tfhdPayload := make([]byte, 4)
	binary.BigEndian.PutUint32(tfhdPayload, trackID)
	tfhd := box("tfhd", fullBox(0, 0, tfhdPayload))

	var tfdtPayload []byte
	if tfdtVersion == 1 {
		tfdtPayload = make([]byte, 8)
		binary.BigEndian.PutUint64(tfdtPayload, decodeTime)
	} else {
		tfdtPayload = make([]byte, 4)
		binary.BigEndian.PutUint32(tfdtPayload, uint32(decodeTime))
	}
	tfdt := box("tfdt", fullBox(tfdtVersion, 0, tfdtPayload))

	traf := box("traf", append(tfhd, tfdt...))
	moof := box("moof", traf)
	mdat := box("mdat", []byte{0x00, 0x01, 0x02, 0x03})
	return append(moof, mdat...)
}

func makeComplexMoof(trackCount int, tfdtVersion byte, decodeTime uint64) []byte {
	if trackCount < 1 {
		trackCount = 1
	}
	moofPayload := make([]byte, 0, trackCount*64)
	for i := 0; i < trackCount; i++ {
		trackID := uint32(i + 1)
		tfhdPayload := make([]byte, 4)
		binary.BigEndian.PutUint32(tfhdPayload, trackID)
		tfhd := box("tfhd", fullBox(0, 0, tfhdPayload))

		var tfdtPayload []byte
		if tfdtVersion == 1 {
			tfdtPayload = make([]byte, 8)
			binary.BigEndian.PutUint64(tfdtPayload, decodeTime+uint64(i*900))
		} else {
			tfdtPayload = make([]byte, 4)
			binary.BigEndian.PutUint32(tfdtPayload, uint32(decodeTime+uint64(i*900)))
		}
		tfdt := box("tfdt", fullBox(tfdtVersion, 0, tfdtPayload))
		traf := box("traf", append(tfhd, tfdt...))
		moofPayload = append(moofPayload, traf...)
	}
	moof := box("moof", moofPayload)
	mdatPayload := make([]byte, 64*1024)
	return append(moof, box("mdat", mdatPayload)...)
}

func firstTfdtValue(segment []byte) (uint64, bool) {
	for off := 0; off < len(segment); {
		size, typ, _, ok := ReadBoxHeader(segment, off)
		if !ok {
			return 0, false
		}
		boxEnd := off + size
		if typ == "moof" {
			for child := off + 8; child < boxEnd; {
				childSize, childType, _, childOK := ReadBoxHeader(segment, child)
				if !childOK {
					return 0, false
				}
				childEnd := child + childSize
				if childType == "traf" {
					for inner := child + 8; inner < childEnd; {
						innerSize, innerType, _, innerOK := ReadBoxHeader(segment, inner)
						if !innerOK {
							return 0, false
						}
						innerEnd := inner + innerSize
						if innerType == "tfdt" {
							version := segment[inner+8]
							if version == 1 {
								return binary.BigEndian.Uint64(segment[inner+12 : inner+20]), true
							}
							return uint64(binary.BigEndian.Uint32(segment[inner+12 : inner+16])), true
						}
						inner = innerEnd
					}
				}
				child = childEnd
			}
		}
		off = boxEnd
	}
	return 0, false
}

func TestNormalizeFragmentTimestamps_Version1(t *testing.T) {
	bases := map[uint32]uint64{}

	seg1 := makeMoof(1, 1, 90000)
	changed1 := NormalizeFragmentTimestamps(seg1, bases)
	if changed1 != 1 {
		t.Fatalf("expected one tfdt normalized in seg1, got %d", changed1)
	}
	got1, ok := firstTfdtValue(seg1)
	if !ok {
		t.Fatal("cannot read tfdt from seg1")
	}
	if got1 != 0 {
		t.Fatalf("expected first tfdt to become 0, got %d", got1)
	}

	seg2 := makeMoof(1, 1, 180000)
	changed2 := NormalizeFragmentTimestamps(seg2, bases)
	if changed2 != 1 {
		t.Fatalf("expected one tfdt normalized in seg2, got %d", changed2)
	}
	got2, ok := firstTfdtValue(seg2)
	if !ok {
		t.Fatal("cannot read tfdt from seg2")
	}
	if got2 != 90000 {
		t.Fatalf("expected second tfdt to become 90000, got %d", got2)
	}
}

func TestNormalizeFragmentTimestamps_Version0(t *testing.T) {
	bases := map[uint32]uint64{}

	seg1 := makeMoof(2, 0, 1000)
	changed1 := NormalizeFragmentTimestamps(seg1, bases)
	if changed1 != 1 {
		t.Fatalf("expected one tfdt normalized in seg1, got %d", changed1)
	}
	got1, ok := firstTfdtValue(seg1)
	if !ok {
		t.Fatal("cannot read tfdt from seg1")
	}
	if got1 != 0 {
		t.Fatalf("expected first tfdt to become 0, got %d", got1)
	}

	seg2 := makeMoof(2, 0, 1600)
	changed2 := NormalizeFragmentTimestamps(seg2, bases)
	if changed2 != 1 {
		t.Fatalf("expected one tfdt normalized in seg2, got %d", changed2)
	}
	got2, ok := firstTfdtValue(seg2)
	if !ok {
		t.Fatal("cannot read tfdt from seg2")
	}
	if got2 != 600 {
		t.Fatalf("expected second tfdt to become 600, got %d", got2)
	}
}

func BenchmarkNormalizeFragmentTimestamps_Version1(b *testing.B) {
	bases := map[uint32]uint64{1: 90000}
	base := makeMoof(1, 1, 180000)
	work := make([]byte, len(base))

	b.ReportAllocs()
	b.SetBytes(int64(len(base)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		copy(work, base)
		_ = NormalizeFragmentTimestamps(work, bases)
	}
}

func BenchmarkNormalizeFragmentTimestamps_Version0(b *testing.B) {
	bases := map[uint32]uint64{2: 1000}
	base := makeMoof(2, 0, 1600)
	work := make([]byte, len(base))

	b.ReportAllocs()
	b.SetBytes(int64(len(base)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		copy(work, base)
		_ = NormalizeFragmentTimestamps(work, bases)
	}
}

func BenchmarkNormalizeFragmentTimestamps_ComplexFragment(b *testing.B) {
	bases := map[uint32]uint64{
		1: 90000,
		2: 45000,
		3: 18000,
	}
	base := makeComplexMoof(3, 1, 180000)
	work := make([]byte, len(base))

	b.ReportAllocs()
	b.SetBytes(int64(len(base)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		copy(work, base)
		_ = NormalizeFragmentTimestamps(work, bases)
	}
}
