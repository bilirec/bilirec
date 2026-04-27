package flv_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/eric2788/bilirec/pkg/flv"
)

func TestFlvHeaderWriter_Prepend_NoOptionalHeaders(t *testing.T) {
	w := &flv.FlvHeaderWriter{}
	out := w.Prepend([]byte{0x09, 0xaa})
	want := len(flv.NewFileHeaderBytes()) + 2
	if len(out) != want {
		t.Fatalf("expected %d bytes, got %d", want, len(out))
	}
	if string(out[len(flv.NewFileHeaderBytes()):]) != string([]byte{0x09, 0xaa}) {
		t.Fatal("payload was not appended correctly")
	}
}

func TestFlvHeaderWriter_Prepend_WithHeaders(t *testing.T) {
	vTag := flv.NewTagBytes(flv.TagTypeVideo, []byte{0x17, 0x00, 0x00})
	aTag := flv.NewTagBytes(flv.TagTypeAudio, []byte{0xaf, 0x00, 0x12})
	w := &flv.FlvHeaderWriter{
		VideoHeaderTag: vTag,
		AudioHeaderTag: aTag,
	}
	payload := []byte{0x09, 0x01}
	out := w.Prepend(payload)
	want := len(flv.NewFileHeaderBytes()) + len(vTag) + len(aTag) + len(payload)
	if len(out) != want {
		t.Fatalf("expected %d bytes, got %d", want, len(out))
	}
}

func TestFlvHeaderWriter_Prepend_NormalizesInjectedHeaderTimestamps(t *testing.T) {
	vTag := flv.NewTagBytes(flv.TagTypeVideo, []byte{0x17, 0x00, 0x00})
	aTag := flv.NewTagBytes(flv.TagTypeAudio, []byte{0xaf, 0x00, 0x12})

	// Simulate headers captured from an ongoing stream with non-zero timestamps.
	vTag[4], vTag[5], vTag[6], vTag[7] = 0x01, 0x02, 0x03, 0x04
	aTag[4], aTag[5], aTag[6], aTag[7] = 0x10, 0x20, 0x30, 0x40

	w := &flv.FlvHeaderWriter{
		VideoHeaderTag: vTag,
		AudioHeaderTag: aTag,
	}
	out := w.Prepend([]byte{0x09})

	preamble := len(flv.NewFileHeaderBytes())
	videoStart := preamble
	audioStart := preamble + len(vTag)

	if out[videoStart+4] != 0 || out[videoStart+5] != 0 || out[videoStart+6] != 0 || out[videoStart+7] != 0 {
		t.Fatal("expected video header timestamp to be normalized to zero")
	}
	if out[audioStart+4] != 0 || out[audioStart+5] != 0 || out[audioStart+6] != 0 || out[audioStart+7] != 0 {
		t.Fatal("expected audio header timestamp to be normalized to zero")
	}

	// Ensure source tags are not mutated by Prepend.
	if vTag[4] == 0 && vTag[5] == 0 && vTag[6] == 0 && vTag[7] == 0 {
		t.Fatal("source video tag should not be mutated")
	}
	if aTag[4] == 0 && aTag[5] == 0 && aTag[6] == 0 && aTag[7] == 0 {
		t.Fatal("source audio tag should not be mutated")
	}
}

func TestFlvHeaderChangedError_Is(t *testing.T) {
	e := &flv.FlvHeaderChangedError{
		VideoHeaderTag: []byte{0x01},
	}
	if !errors.Is(e, flv.ErrVideoHeaderChanged) {
		t.Fatal("FlvHeaderChangedError should match ErrVideoHeaderChanged via errors.Is")
	}
}

func TestRealtimeFixer_SkipsSourceHeader(t *testing.T) {
	fixer := flv.NewRealtimeFixer()
	defer fixer.Close()

	out, err := fixer.Fix(flv.FlvHeader)
	if err != nil {
		t.Fatalf("unexpected error on FLV header input: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected no output when only FLV file header is consumed, got %d bytes", len(out))
	}
}

func TestRealtimeFixer_MidStream_NoHeaderInInput(t *testing.T) {
	// Simulate segment N: reset marks the next input as a rotation segment
	// that starts at a tag boundary without an FLV file header.
	fixer := flv.NewRealtimeFixer()
	defer fixer.Close()
	fixer.ResetTimestampStore()

	// Rotation-segment input starts with PreviousTagSize of the prior tag,
	// followed by a full tag (header + payload + trailing PreviousTagSize).
	tag := flv.NewTagBytes(flv.TagTypeAudio, []byte{0xaf, 0x01, 0x11})
	in := make([]byte, 0, flv.PrevTagSizeBytes+len(tag))
	in = append(in, 0, 0, 0, 0)
	in = append(in, tag...)

	out, err := fixer.Fix(in)
	if err != nil {
		t.Fatalf("rotation-segment mid-stream data should not produce an error, got: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected processed output for valid mid-stream rotation-segment tag")
	}
}

func TestRealtimeFixer_ClampsNegativeTimestampAfterReset(t *testing.T) {
	fixer := flv.NewRealtimeFixer()
	defer fixer.Close()
	fixer.ResetTimestampStore()

	// Build two tags where the second tag timestamp goes backward slightly.
	tag1 := flv.NewTagBytes(flv.TagTypeAudio, []byte{0xaf, 0x01, 0x11})
	tag1[4], tag1[5], tag1[6], tag1[7] = 0x00, 0x01, 0x2c, 0x00 // 300ms

	tag2 := flv.NewTagBytes(flv.TagTypeAudio, []byte{0xaf, 0x01, 0x22})
	tag2[4], tag2[5], tag2[6], tag2[7] = 0x00, 0x01, 0x2a, 0x00 // 298ms

	in := make([]byte, 0, flv.PrevTagSizeBytes+len(tag1)+len(tag2))
	in = append(in, 0, 0, 0, 0)
	in = append(in, tag1...)
	in = append(in, tag2...)
	out, err := fixer.Fix(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(out) < (flv.TagHeaderSize+3+flv.PrevTagSizeBytes)*2 {
		t.Fatalf("unexpected output size: %d", len(out))
	}

	firstTagLen := flv.TagHeaderSize + 3 + flv.PrevTagSizeBytes
	secondHeader := out[firstTagLen : firstTagLen+flv.TagHeaderSize]
	secondTs := uint32(secondHeader[7])<<24 | uint32(secondHeader[4])<<16 | uint32(secondHeader[5])<<8 | uint32(secondHeader[6])
	if secondTs > 1000 {
		t.Fatalf("expected clamped non-negative timestamp for second tag, got %dms", secondTs)
	}

	// Guard against wrapped value near uint32 max.
	if secondTs > 0xF0000000 {
		t.Fatalf("timestamp wrapped unexpectedly: %d", secondTs)
	}

	// Ensure PrevTagSize is still valid for the second tag.
	secondPrev := binary.BigEndian.Uint32(out[firstTagLen+flv.TagHeaderSize+3 : firstTagLen+flv.TagHeaderSize+3+flv.PrevTagSizeBytes])
	if secondPrev != uint32(flv.TagHeaderSize+3) {
		t.Fatalf("unexpected prev tag size: %d", secondPrev)
	}
}

func TestNewTagBytes_BuildsPrevTagSize(t *testing.T) {
	payload := []byte{0x17, 0x00, 0x00}
	tag := flv.NewTagBytes(flv.TagTypeVideo, payload)
	if len(tag) != flv.TagHeaderSize+len(payload)+flv.PrevTagSizeBytes {
		t.Fatalf("unexpected tag length: %d", len(tag))
	}
	if got := uint32(tag[len(tag)-4])<<24 | uint32(tag[len(tag)-3])<<16 | uint32(tag[len(tag)-2])<<8 | uint32(tag[len(tag)-1]); got != uint32(flv.TagHeaderSize+len(payload)) {
		t.Fatalf("unexpected prev tag size: %d", got)
	}
}

func TestTagPool_ClearsDataOnPut(t *testing.T) {
	// Put a tag with a large Data slice into the pool

	tag := &flv.Tag{}
	tag.Data = make([]byte, 1024*1024) // 1MB
	tag.Reset()
	if tag.Data != nil {
		t.Fatalf("Reset did not clear Data: len=%d cap=%d", len(tag.Data), cap(tag.Data))
	}
}

func TestHeaderChangeDetector_DetectsSequenceHeaderChange(t *testing.T) {
	d := flv.NewHeaderChangeDetector()

	h1 := []byte{0x17, 0x00, 0x00, 0x00, 0x00, 0x01, 0x42, 0x00, 0x1e}
	h2 := []byte{0x17, 0x00, 0x00, 0x00, 0x00, 0x01, 0x42, 0x00, 0x2a}

	chunk1 := buildFLVVideoTag(h1)
	chunk2 := buildFLVVideoTag(h2)

	if err := d.DetectChange(chunk1); err != nil {
		t.Fatalf("first sequence header should not trigger split, got err: %v", err)
	}

	err := d.DetectChange(chunk2)
	if !errors.Is(err, flv.ErrVideoHeaderChanged) {
		t.Fatalf("expected ErrVideoHeaderChanged, got: %v", err)
	}
}

func TestHeaderChangeDetector_DetectsSequenceHeaderChangeAcrossChunks(t *testing.T) {
	d := flv.NewHeaderChangeDetector()

	h1 := []byte{0x17, 0x00, 0x00, 0x00, 0x00, 0x01, 0x42, 0x00, 0x1e}
	h2 := []byte{0x17, 0x00, 0x00, 0x00, 0x00, 0x01, 0x64, 0x00, 0x29}

	tag1 := buildFLVVideoTag(h1)
	tag2 := buildFLVVideoTag(h2)

	if err := d.DetectChange(tag1); err != nil {
		t.Fatalf("first sequence header should not trigger split, got err: %v", err)
	}

	// Split second tag across chunks to emulate network boundary split.
	splitAt := len(tag2) / 2
	if err := d.DetectChange(tag2[:splitAt]); err != nil {
		t.Fatalf("partial chunk should not trigger split yet, got err: %v", err)
	}

	err := d.DetectChange(tag2[splitAt:])
	if !errors.Is(err, flv.ErrVideoHeaderChanged) {
		t.Fatalf("expected ErrVideoHeaderChanged across chunks, got: %v", err)
	}
}

func TestHeaderChangeDetector_KeepsPendingWhenPrevTagSizeSplitAcrossChunks(t *testing.T) {
	d := flv.NewHeaderChangeDetector()

	h1 := []byte{0x17, 0x00, 0x00, 0x00, 0x00, 0x01, 0x42, 0x00, 0x1e}
	h2 := []byte{0x17, 0x00, 0x00, 0x00, 0x00, 0x01, 0x64, 0x00, 0x29}

	tag1 := buildFLVVideoTag(h1)
	tag2 := buildFLVVideoTag(h2)

	if err := d.DetectChange(tag1[:len(tag1)-flv.PrevTagSizeBytes]); err != nil {
		t.Fatalf("partial first tag ending before PrevTagSize should not fail, got err: %v", err)
	}

	err := d.DetectChange(append(tag1[len(tag1)-flv.PrevTagSizeBytes:], tag2...))
	if !errors.Is(err, flv.ErrVideoHeaderChanged) {
		t.Fatalf("expected ErrVideoHeaderChanged after replaying split PrevTagSize, got: %v", err)
	}
}

func TestHeaderChangeDetector_IgnoresNonSequenceVideo(t *testing.T) {
	d := flv.NewHeaderChangeDetector()

	// AVCPacketType = 1, not sequence header
	videoNalu := []byte{0x17, 0x01, 0x00, 0x00, 0x00, 0xaa, 0xbb, 0xcc}
	chunk := buildFLVVideoTag(videoNalu)

	if err := d.DetectChange(chunk); err != nil {
		t.Fatalf("non-sequence video should not trigger split, got err: %v", err)
	}
}

func TestHeaderChangeDetector_LastVideoHeaderReturnsCopy(t *testing.T) {
	d := flv.NewHeaderChangeDetector()

	h := []byte{0x17, 0x00, 0x00, 0x00, 0x00, 0x01, 0x42, 0x00, 0x1e}
	if err := d.DetectChange(buildFLVVideoTag(h)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	a := d.LastVideoHeader()
	if len(a) == 0 {
		t.Fatal("expected non-empty last video header")
	}
	a[0] ^= 0xff

	b := d.LastVideoHeader()
	if a[0] == b[0] {
		t.Fatal("LastVideoHeader should return a copy, got shared backing array")
	}
}

func buildFLVVideoTag(payload []byte) []byte {
	dataSize := len(payload)
	tagHeader := make([]byte, 11)
	tagHeader[0] = flv.TagTypeVideo
	tagHeader[1] = byte(dataSize >> 16)
	tagHeader[2] = byte(dataSize >> 8)
	tagHeader[3] = byte(dataSize)

	out := make([]byte, 0, 11+dataSize+4)
	out = append(out, tagHeader...)
	out = append(out, payload...)

	prevTagSize := uint32(11 + dataSize)
	out = append(out,
		byte(prevTagSize>>24),
		byte(prevTagSize>>16),
		byte(prevTagSize>>8),
		byte(prevTagSize),
	)
	return out
}
