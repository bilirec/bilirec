package preview

import (
	"encoding/binary"
	"io"
	"os"

	"github.com/bilirec/bilirec/pkg/flv"
)

const (
	flvJoinHeaderScan = 256 * 1024
	flvJoinLookback   = 8 * 1024 * 1024
)

type liveJoin struct {
	prefix []byte
	offset int64
	baseTS uint32
}

// flvLiveJoin builds a playable FLV prefix (file header + sequence headers)
// and the byte offset of a recent media keyframe so a late viewer can start
// near the live edge instead of replaying the whole file. baseTS is the
// original keyframe timestamp; followFile subtracts it so the player clock
// starts at 0 on the latest picture.
func flvLiveJoin(path string) (liveJoin, bool) {
	f, err := os.Open(path)
	if err != nil {
		return liveJoin{}, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() <= int64(flv.FlvHeaderSize+flv.PrevTagSizeBytes) {
		return liveJoin{}, false
	}

	scan := flvJoinHeaderScan
	if int64(scan) > st.Size() {
		scan = int(st.Size())
	}
	head := make([]byte, scan)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.ErrUnexpectedEOF && n == 0 {
		return liveJoin{}, false
	}
	head = head[:n]

	det := flv.NewHeaderChangeDetector()
	defer det.Close()
	_ = det.DetectChange(head)
	vTag := zeroFLVTagTS(det.LastVideoTag())
	if len(vTag) == 0 {
		return liveJoin{}, false
	}

	prefix := flv.NewFileHeaderBytes()
	prefix = append(prefix, vTag...)
	if aTag := zeroFLVTagTS(det.LastAudioTag()); len(aTag) > 0 {
		prefix = append(prefix, aTag...)
	}

	off, ts, found := findRecentKeyframeOffset(f, st.Size())
	if !found {
		return liveJoin{}, false
	}
	return liveJoin{prefix: prefix, offset: off, baseTS: ts}, true
}

func findRecentKeyframeOffset(f *os.File, size int64) (int64, uint32, bool) {
	lookback := int64(flvJoinLookback)
	if lookback > size {
		lookback = size
	}
	start := size - lookback
	buf := make([]byte, lookback)
	n, err := f.ReadAt(buf, start)
	if err != nil && err != io.EOF && n == 0 {
		return 0, 0, false
	}
	buf = buf[:n]

	off := alignFLVTag(buf)
	if off < 0 {
		return 0, 0, false
	}
	keyRel := -1
	forEachFLVTag(buf[off:], func(rel int, tagType byte, data []byte) bool {
		if tagType != flv.TagTypeVideo {
			return true
		}
		_, isHeader, isKey := flv.ClassifyVideoTag(data)
		if isKey && !isHeader {
			keyRel = off + rel
		}
		return true
	})
	if keyRel < 0 {
		return 0, 0, false
	}
	return start + int64(keyRel), flvTagTS(buf[keyRel:]), true
}

func flvTagTS(tag []byte) uint32 {
	if len(tag) < 8 {
		return 0
	}
	return uint32(tag[4])<<16 | uint32(tag[5])<<8 | uint32(tag[6]) | uint32(tag[7])<<24
}

func setFLVTagTS(tag []byte, ts uint32) {
	if len(tag) < 8 {
		return
	}
	tag[4] = byte(ts >> 16)
	tag[5] = byte(ts >> 8)
	tag[6] = byte(ts)
	tag[7] = byte(ts >> 24)
}

func zeroFLVTagTS(tag []byte) []byte {
	if len(tag) == 0 {
		return nil
	}
	out := append([]byte(nil), tag...)
	setFLVTagTS(out, 0)
	return out
}

// tsRebaser rewrites FLV tag timestamps so a late-join stream starts at 0.
type tsRebaser struct {
	dst  io.Writer
	base uint32
	buf  []byte
}

func (r *tsRebaser) Write(p []byte) (int, error) {
	if r.base == 0 {
		return r.dst.Write(p)
	}
	r.buf = append(r.buf, p...)
	for {
		if len(r.buf) < flv.TagHeaderSize {
			break
		}
		dataSize := int(r.buf[1])<<16 | int(r.buf[2])<<8 | int(r.buf[3])
		if dataSize <= 0 || dataSize > flv.MaxTagDataSize {
			// Not aligned; drop one byte and resync rather than stalling.
			r.buf = r.buf[1:]
			continue
		}
		total := flv.TagHeaderSize + dataSize + flv.PrevTagSizeBytes
		if len(r.buf) < total {
			break
		}
		tag := r.buf[:total]
		ts := flvTagTS(tag)
		if ts >= r.base {
			setFLVTagTS(tag, ts-r.base)
		} else {
			setFLVTagTS(tag, 0)
		}
		if _, err := r.dst.Write(tag); err != nil {
			return len(p), err
		}
		r.buf = r.buf[total:]
	}
	return len(p), nil
}

func alignFLVTag(buf []byte) int {
	for i := 0; i+flv.TagHeaderSize+flv.PrevTagSizeBytes <= len(buf); i++ {
		if validFLVTagAt(buf, i) {
			return i
		}
	}
	return -1
}

func validFLVTagAt(buf []byte, i int) bool {
	if i+flv.TagHeaderSize > len(buf) {
		return false
	}
	tagType := buf[i]
	if tagType != flv.TagTypeAudio && tagType != flv.TagTypeVideo && tagType != flv.TagTypeScript {
		return false
	}
	dataSize := int(buf[i+1])<<16 | int(buf[i+2])<<8 | int(buf[i+3])
	if dataSize <= 0 || dataSize > flv.MaxTagDataSize {
		return false
	}
	end := i + flv.TagHeaderSize + dataSize + flv.PrevTagSizeBytes
	if end > len(buf) {
		return false
	}
	prev := binary.BigEndian.Uint32(buf[end-flv.PrevTagSizeBytes:])
	return int(prev) == flv.TagHeaderSize+dataSize
}

func forEachFLVTag(buf []byte, fn func(off int, tagType byte, data []byte) bool) {
	off := 0
	for validFLVTagAt(buf, off) {
		dataSize := int(buf[off+1])<<16 | int(buf[off+2])<<8 | int(buf[off+3])
		data := buf[off+flv.TagHeaderSize : off+flv.TagHeaderSize+dataSize]
		if !fn(off, buf[off], data) {
			return
		}
		off += flv.TagHeaderSize + dataSize + flv.PrevTagSizeBytes
	}
}
