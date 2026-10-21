package preview

import (
	"github.com/bilirec/bilirec/pkg/flv"
)

// joinFilter turns the live pipeline into a playable late-join stream for one viewer.
type joinFilter struct {
	buf     []byte
	started bool
	baseTS  uint32
}

func (f *joinFilter) feed(ext string, chunk, bootstrap []byte) []byte {
	switch ext {
	case ".flv":
		return f.feedFLV(chunk, bootstrap)
	case ".fmp4":
		return f.feedFMP4(chunk, bootstrap)
	default:
		return chunk
	}
}

func (f *joinFilter) feedFMP4(chunk, bootstrap []byte) []byte {
	if isFtyp(chunk) {
		return nil
	}
	if !f.started {
		if len(bootstrap) == 0 {
			return nil
		}
		f.started = true
		out := make([]byte, 0, len(bootstrap)+len(chunk))
		out = append(out, bootstrap...)
		out = append(out, chunk...)
		return out
	}
	return chunk
}

func (f *joinFilter) feedFLV(chunk, bootstrap []byte) []byte {
	if len(f.buf) == 0 && len(chunk) >= flv.FlvHeaderSize && string(chunk[:3]) == "FLV" {
		skip := flv.FlvHeaderSize + flv.PrevTagSizeBytes
		if skip > len(chunk) {
			return nil
		}
		chunk = chunk[skip:]
	}
	f.buf = append(f.buf, chunk...)
	var out []byte
	for {
		tag, rest, ok, skip := flv.NextTag(f.buf)
		if skip {
			f.buf = rest
			continue
		}
		if !ok {
			break
		}
		f.buf = rest
		if !f.started {
			if !isFLVKeyframe(tag) {
				continue
			}
			if len(bootstrap) == 0 {
				continue
			}
			f.baseTS = flv.TagTimestamp(tag)
			f.started = true
			out = append(out, bootstrap...)
			out = appendRebased(out, tag, f.baseTS)
			continue
		}
		out = appendRebased(out, tag, f.baseTS)
	}
	return out
}

func isFLVKeyframe(tag []byte) bool {
	if len(tag) == 0 || tag[0] != flv.TagTypeVideo {
		return false
	}
	_, isHeader, isKey := flv.ClassifyVideoTag(flv.TagData(tag))
	return isKey && !isHeader
}

func appendRebased(dst, tag []byte, base uint32) []byte {
	start := len(dst)
	dst = append(dst, tag...)
	ts := flv.TagTimestamp(dst[start:])
	if ts >= base {
		flv.SetTagTimestamp(dst[start:], ts-base)
	} else {
		flv.SetTagTimestamp(dst[start:], 0)
	}
	return dst
}
