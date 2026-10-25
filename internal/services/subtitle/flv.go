package subtitle

import (
	"github.com/bilirec/bilirec/pkg/flv"
)

// accessUnitsFromFLVChunk 把录制管道里的一段 FLV 字节（可能含多个 tag）拆成 accessUnit。
// 只处理视频 tag；sequence header 与音频 tag 跳过。
func accessUnitsFromFLVChunk(data []byte) []accessUnit {
	var out []accessUnit
	rest := data
	for len(rest) > 0 {
		tag, next, ok, skip := flv.NextTag(rest)
		if !ok {
			break
		}
		rest = next
		if skip || len(tag) == 0 || tag[0] != flv.TagTypeVideo {
			continue
		}
		body := flv.TagData(tag)
		kind, isHeader, _ := flv.ClassifyVideoTag(body)
		if isHeader || kind == flv.VideoKindNone {
			continue
		}
		nals, ok := videoNALPayload(body, kind)
		if !ok || len(nals) == 0 {
			continue
		}
		// FLV tag 时间戳 + composition time（毫秒）≈ 该帧显示时刻。
		pts := int64(flv.TagTimestamp(tag)) + compositionTimeMs(body, kind)
		out = append(out, accessUnit{PTSMs: pts, Codec: kind, NALs: nals})
	}
	return out
}

// compositionTimeMs 从 FLV 视频 tag body 读出 composition time（signed 24-bit，毫秒）。
func compositionTimeMs(body []byte, kind flv.VideoKind) int64 {
	var off int
	switch kind {
	case flv.VideoKindHEVCEnhanced:
		// Enhanced RTMP：packet type 在 body[5]，CT 在 body[6:9]。
		if len(body) < 9 {
			return 0
		}
		if body[5] != 1 {
			return 0
		}
		off = 6
	default:
		// AVC/HEVC legacy：AVCPacketType==1 表示 NALU，CT 在 body[2:5]。
		if len(body) < 6 || body[1] != 1 {
			return 0
		}
		off = 2
	}
	return int64(int24(body[off : off+3]))
}

func int24(b []byte) int32 {
	v := int32(b[0])<<16 | int32(b[1])<<8 | int32(b[2])
	if v&0x800000 != 0 {
		v |= -0x1000000
	}
	return v
}

// videoNALPayload 去掉 FLV 视频 tag 里的封装头，返回 AVCC 格式的 NAL 串（长度前缀 NAL）。
func videoNALPayload(body []byte, kind flv.VideoKind) ([]byte, bool) {
	switch kind {
	case flv.VideoKindHEVCEnhanced:
		if len(body) < 9 || body[5] != 1 {
			return nil, false
		}
		return body[9:], true
	case flv.VideoKindAVC, flv.VideoKindHEVCLegacy:
		if len(body) < 6 || body[1] != 1 {
			return nil, false
		}
		return body[5:], true
	default:
		return nil, false
	}
}
