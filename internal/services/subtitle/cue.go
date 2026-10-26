package subtitle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/sei"
)

const maxSEIRBSPBytes = 64 * 1024

func videoSEICodec(kind flv.VideoKind) sei.Codec {
	switch kind {
	case flv.VideoKindAVC:
		return sei.AVC
	case flv.VideoKindHEVCLegacy, flv.VideoKindHEVCEnhanced:
		return sei.HEVC
	default:
		return 0
	}
}

// B 站直播 AI 字幕在 SEI user_data_unregistered 里的 16 字节 UUID，后面紧跟 JSON 数组。
var subtitleUUID = []byte("BILILIVESUBTITLE")

type rawCue struct {
	Sub   string `json:"sub"`
	SubID int64  `json:"sub_id"`
	OTMs  int64  `json:"ot_ms"`
	DTMs  int64  `json:"dt_ms"`
	Type  int    `json:"type"`
}

type cue struct {
	ID    int64
	Text  string
	PTSMs int64
	OTMs  int64
	DTMs  int64
}

// cuesFromSEI 在 SEI payload 中匹配 UUID，解析 JSON（sub、sub_id、ot_ms、dt_ms）。
func cuesFromSEI(au accessUnit) []cue {
	if bytes.Contains(au.NALs, subtitleUUID) {
		if embedded := cuesFromEmbeddedUUID(au.NALs, au.PTSMs); len(embedded) > 0 {
			return embedded
		}
	}
	var out []cue
	sei.ForEachRBSP(au.NALs, videoSEICodec(au.Codec), func(rbsp []byte) {
		if len(rbsp) == 0 || len(rbsp) > maxSEIRBSPBytes {
			return
		}
		out = append(out, cuesFromSEIRBSP(au.PTSMs, rbsp)...)
	})
	if len(out) == 0 {
		out = cuesFromEmbeddedUUID(au.NALs, au.PTSMs)
	}
	return out
}

// cuesFromEmbeddedUUID 在 AVCC/原始 mdat 中直接查找 BILILIVESUBTITLE + JSON（部分 fMP4 片内 SEI 封装与 FLV 不一致）。
func cuesFromEmbeddedUUID(data []byte, ptsMs int64) []cue {
	var out []cue
	off := 0
	for {
		i := bytes.Index(data[off:], subtitleUUID)
		if i < 0 {
			break
		}
		start := off + i + len(subtitleUUID)
		rest := data[start:]
		bracket := bytes.IndexByte(rest, '[')
		if bracket < 0 {
			off = start
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(rest[bracket:]))
		var items []rawCue
		if err := dec.Decode(&items); err != nil {
			off = start
			continue
		}
		for _, it := range items {
			text := strings.TrimSpace(it.Sub)
			if text == "" {
				continue
			}
			out = append(out, cue{
				ID:    it.SubID,
				Text:  text,
				PTSMs: ptsMs,
				OTMs:  it.OTMs,
				DTMs:  it.DTMs,
			})
		}
		off = start + bracket + int(dec.InputOffset())
	}
	return out
}

// cuesFromSEIRBSP 解析已拷贝的 SEI RBSP（在异步写盘 goroutine 中调用）。
func cuesFromSEIRBSP(ptsMs int64, rbsp []byte) []cue {
	var out []cue
	for _, payload := range sei.UserDataUnregistered(rbsp) {
		if len(payload) < len(subtitleUUID) || !bytesHasPrefix(payload, subtitleUUID) {
			continue
		}
		jsonPart := payload[len(subtitleUUID):]
		var items []rawCue
		if err := json.Unmarshal(jsonPart, &items); err != nil {
			continue
		}
		for _, it := range items {
			text := strings.TrimSpace(it.Sub)
			if text == "" {
				continue
			}
			out = append(out, cue{
				ID:    it.SubID,
				Text:  text,
				PTSMs: ptsMs,
				OTMs:  it.OTMs,
				DTMs:  it.DTMs,
			})
		}
	}
	return out
}

func bytesHasPrefix(b, prefix []byte) bool {
	return len(b) >= len(prefix) && string(b[:len(prefix)]) == string(prefix)
}

// formatVTTCue 编码一条 WebVTT cue 块（时间轴行 + 文本 + 空行分隔）。
func formatVTTCue(startMs, endMs int64, text string) []byte {
	return fmt.Appendf(nil, "%s --> %s\n%s\n\n", formatVTTTime(startMs), formatVTTTime(endMs), text)
}

func formatVTTTime(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	h := ms / 3_600_000
	ms %= 3_600_000
	m := ms / 60_000
	ms %= 60_000
	s := ms / 1000
	ms %= 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}
