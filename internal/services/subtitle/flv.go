package subtitle

import (
	"github.com/bilirec/bilirec/pkg/flv"
)

// accessUnitsFromFLVChunk 把录制管道里的一段 FLV 字节（可能含多个 tag）拆成 accessUnit。
// 只处理视频 tag；sequence header 与音频 tag 跳过。
//
// 每个分段的首个 chunk 带 13 字节 FLV 文件前导（flv-header-writer 在字幕扫描
// 之前拼上，见 record_strategies/flv.go 的管线顺序），其余位置也可能遇到非 tag
// 字节。NextTag 对这类字节返回 skip=true 并给出 buf[1:]，这里据此逐字节重同步；
// 之前遇 !ok 直接 break，会导致含前导的首个 chunk 被整体丢弃。
func accessUnitsFromFLVChunk(data []byte) []accessUnit {
	var out []accessUnit
	rest := data
	for len(rest) > 0 {
		tag, next, ok, skip := flv.NextTag(rest)
		if !ok {
			if skip {
				// 非 tag 字节（文件前导、垃圾前缀）：跳过 1 字节重同步。
				rest = next
				continue
			}
			// 剩余字节不足以构成完整 tag：管线 chunk 均为完整 tag 对齐
			//（上游 fixer 按 tag 解析重组），正常不会发生，防御性收尾。
			break
		}
		rest = next
		if len(tag) == 0 || tag[0] != flv.TagTypeVideo {
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
