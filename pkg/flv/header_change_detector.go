package flv

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// ErrVideoHeaderChanged signals that the video sequence header has
// changed and the recording pipeline should rotate to a new file.
var ErrVideoHeaderChanged = errors.New("视频序列头已变化，需要进行管道轮转")

// FlvHeaderChangedError is a rich error returned by HeaderChangeDetector when
// the video sequence header changes. It carries the new tag bytes so callers
// can inject them into the next segment without an extra getter call.
type FlvHeaderChangedError struct {
	// VideoHeaderTag is the full FLV tag bytes (TagHeader + Data + PrevTagSize)
	// for the new video sequence header that triggered rotation.
	VideoHeaderTag []byte
	// AudioHeaderTag is the most recently seen full audio sequence header FLV
	// tag bytes, or nil if no audio sequence header has been observed yet.
	AudioHeaderTag []byte
	// TagOffset is the byte offset of the video seq header tag within the
	// original data slice passed to DetectChange. Split-detector processors
	// can use this to excise the tag from the data before forwarding it.
	TagOffset int
	// TagEnd is the byte offset one past the end of the excised video seq
	// header region, including the trailing PrevTagSize bytes.
	TagEnd int
}

func (e *FlvHeaderChangedError) Error() string {
	return ErrVideoHeaderChanged.Error()
}

// Is allows errors.Is(err, ErrVideoHeaderChanged) to return true.
func (e *FlvHeaderChangedError) Is(target error) bool {
	return target == ErrVideoHeaderChanged
}

// HeaderChangeDetector performs byte-level comparison of FLV video (and
// optionally audio) sequence headers within a raw FLV byte stream.
//
// It is stateful: call Reset() when starting a new recording session.
type HeaderChangeDetector struct {
	lastVideoHeader []byte
	lastVideoTag    []byte
	lastAudioHeader []byte
	lastAudioTag    []byte
	pending         []byte
	buf             *bytes.Buffer // reusable scratch buffer for DetectChange
	tagBuf          *bytes.Buffer // reusable scratch buffer for building tag+PrevTagSize
}

// NewHeaderChangeDetector returns a ready-to-use detector.
func NewHeaderChangeDetector() *HeaderChangeDetector {
	return &HeaderChangeDetector{
		buf:    headerDetectorBufferPool.Get(),
		tagBuf: headerDetectorBufferPool.Get(),
	}
}

// Reset clears remembered headers (call at the start of a new recording file).
func (d *HeaderChangeDetector) Reset() {
	d.lastVideoHeader = nil
	d.lastVideoTag = nil
	d.lastAudioHeader = nil
	d.lastAudioTag = nil
	d.pending = nil
	if d.buf != nil {
		d.buf.Reset()
	}
	if d.tagBuf != nil {
		d.tagBuf.Reset()
	}
}

// Close releases the internal pooled buffers back to the pool and resets all
// session state. Call this when the detector is no longer needed (e.g. recording
// stopped). After Close, the detector must not be used again.
func (d *HeaderChangeDetector) Close() {
	d.lastVideoHeader = nil
	d.lastVideoTag = nil
	d.lastAudioHeader = nil
	d.lastAudioTag = nil
	d.pending = nil
	if d.buf != nil {
		d.buf.Reset()
		headerDetectorBufferPool.Put(d.buf)
		d.buf = nil
	}
	if d.tagBuf != nil {
		d.tagBuf.Reset()
		headerDetectorBufferPool.Put(d.tagBuf)
		d.tagBuf = nil
	}
}

// SeedVideoHeader pre-seeds the detector with a known video sequence header so
// it can detect changes from a mid-stream header injected by a prior rotation.
// Pass the full FLV tag bytes (TagHeader + TagData + PrevTagSize) as stored in
// FlvHeaderChangedError.VideoHeaderTag. Non-header payloads are ignored.
// Must be called after Reset().
func (d *HeaderChangeDetector) SeedVideoHeader(fullTagBytes []byte) {
	if len(fullTagBytes) <= TagHeaderSize+PrevTagSizeBytes {
		return
	}
	tagData := fullTagBytes[TagHeaderSize : len(fullTagBytes)-PrevTagSizeBytes]
	_, isHeader, _ := ClassifyVideoTag(tagData)
	if !isHeader {
		return
	}
	d.lastVideoHeader = append([]byte(nil), tagData...)
	d.lastVideoTag = append([]byte(nil), fullTagBytes...)
}

// LastVideoHeader returns a copy of the most recently seen video sequence
// header tag data, or nil if none has been seen yet.
// Callers can inject this into a new file on pipe rotation.
func (d *HeaderChangeDetector) LastVideoHeader() []byte {
	if d.lastVideoHeader == nil {
		return nil
	}
	return append([]byte(nil), d.lastVideoHeader...)
}

// LastVideoTag returns a copy of the most recently seen video sequence
// header FLV tag bytes (TagHeader + TagData + PreviousTagSize), or nil.
func (d *HeaderChangeDetector) LastVideoTag() []byte {
	if d.lastVideoTag == nil {
		return nil
	}
	return append([]byte(nil), d.lastVideoTag...)
}

// LastAudioHeader returns a copy of the most recently seen audio sequence
// header tag data, or nil if none has been seen yet.
func (d *HeaderChangeDetector) LastAudioHeader() []byte {
	if d.lastAudioHeader == nil {
		return nil
	}
	return append([]byte(nil), d.lastAudioHeader...)
}

// LastAudioTag returns a copy of the most recently seen audio sequence header
// FLV tag bytes (TagHeader + TagData + PreviousTagSize), or nil.
func (d *HeaderChangeDetector) LastAudioTag() []byte {
	if d.lastAudioTag == nil {
		return nil
	}
	return append([]byte(nil), d.lastAudioTag...)
}

// DetectChange scans a raw FLV byte chunk for sequence-header tags.
// It returns ErrVideoHeaderChanged the first time a video sequence header
// with different binary content is found compared to the previously seen one.
// The detector remembers the most recently seen sequence headers, including
// the new video header that triggered ErrVideoHeaderChanged, so callers can
// retrieve it after rotation; call Reset() when starting a new recording file.
func (d *HeaderChangeDetector) DetectChange(data []byte) error {
	carryLen := len(d.pending)
	d.buf.Reset()
	if carryLen > 0 {
		d.buf.Write(d.pending)
	}
	d.buf.Write(data)
	buf := d.buf.Bytes()

	offset := 0

	// Skip FLV file header + PreviousTagSize0 if this chunk starts with "FLV"
	if carryLen == 0 && len(buf) >= FlvHeaderSize && buf[0] == 'F' && buf[1] == 'L' && buf[2] == 'V' {
		offset = FlvHeaderSize + PrevTagSizeBytes
	}

	for offset+TagHeaderSize < len(buf) {
		tagType := buf[offset]
		dataSize := int(buf[offset+1])<<16 | int(buf[offset+2])<<8 | int(buf[offset+3])
		tagEnd := offset + TagHeaderSize + dataSize
		tagBoundaryEnd := tagEnd + PrevTagSizeBytes

		if tagBoundaryEnd > len(buf) {
			break
		}

		tagData := buf[offset+TagHeaderSize : tagEnd]
		tagBytes := d.tagWithPrevSize(buf[offset:tagEnd])

		switch tagType {
		case TagTypeVideo:
			if err := d.checkVideoHeader(tagData, tagBytes, offset, tagEnd); err != nil {
				if changed, ok := err.(*FlvHeaderChangedError); ok && carryLen > 0 {
					changed.TagOffset -= carryLen
					changed.TagEnd -= carryLen
					if changed.TagOffset < 0 {
						changed.TagOffset = 0
					}
					if changed.TagEnd < 0 {
						changed.TagEnd = 0
					}
				}
				d.pending = nil
				return err
			}
		case TagTypeAudio:
			if err := d.checkAudioHeader(tagData, tagBytes); err != nil {
				d.pending = nil
				return err
			}
		}

		// Advance: TagHeaderSize + dataSize + PreviousTagSize(4)
		offset = tagBoundaryEnd
	}

	if offset < len(buf) {
		d.pending = append(d.pending[:0], buf[offset:]...)
	} else {
		d.pending = nil
	}

	return nil
}

// tagWithPrevSize builds a tag+PrevTagSize byte slice into the reusable
// d.tagBuf and returns d.tagBuf.Bytes(). The returned slice is valid only
// until the next call to tagWithPrevSize; callers must copy it if they need
// to retain the data (they all do via append([]byte(nil), ...)).
func (d *HeaderChangeDetector) tagWithPrevSize(src []byte) []byte {
	d.tagBuf.Reset()
	d.tagBuf.Write(src)
	var sz [PrevTagSizeBytes]byte
	binary.BigEndian.PutUint32(sz[:], uint32(len(src)))
	d.tagBuf.Write(sz[:])
	return d.tagBuf.Bytes()
}

// checkVideoHeader inspects a video tag's payload for sequence header
// changes (AVC, HEVC codec id 12, or Enhanced FLV hvc1/hev1).
// Returns ErrVideoHeaderChanged on a binary diff.
func (d *HeaderChangeDetector) checkVideoHeader(tagData []byte, tagBytes []byte, tagOffset int, tagEnd int) error {
	_, isHeader, _ := ClassifyVideoTag(tagData)
	if !isHeader {
		return nil
	}

	if d.lastVideoHeader == nil {
		// First time: remember and continue.
		d.lastVideoHeader = append([]byte(nil), tagData...)
		d.lastVideoTag = append([]byte(nil), tagBytes...)
		return nil
	}

	if !bytes.Equal(d.lastVideoHeader, tagData) {
		// Header changed — update state so the caller can retrieve the new
		// header via LastVideoHeader() after handling the error.
		d.lastVideoHeader = append([]byte(nil), tagData...)
		d.lastVideoTag = append([]byte(nil), tagBytes...)
		return &FlvHeaderChangedError{
			VideoHeaderTag: append([]byte(nil), tagBytes...),
			AudioHeaderTag: d.LastAudioTag(),
			TagOffset:      tagOffset,
			TagEnd:         tagEnd + PrevTagSizeBytes,
		}
	}

	return nil
}

// checkAudioHeader records AAC sequence header tag bytes.
func (d *HeaderChangeDetector) checkAudioHeader(tagData []byte, tagBytes []byte) error {
	if len(tagData) < 2 {
		return nil
	}
	// tagData[0] >> 4 == 10 → AAC; tagData[1] == 0 → AAC sequence header
	if (tagData[0]>>4) != 10 || tagData[1] != 0x00 {
		return nil
	}
	// Always update — both tagData and full tag bytes.
	d.lastAudioHeader = append([]byte(nil), tagData...)
	d.lastAudioTag = append([]byte(nil), tagBytes...)
	return nil
}
