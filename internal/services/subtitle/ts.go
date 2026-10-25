package subtitle

import (
	"github.com/bilirec/bilirec/pkg/flv"
)

const tsPacketSize = 188

// accessUnitsFromTSChunk 解析 HLS MPEG-TS 分段：188 字节一包，拼 PES，取出 Annex-B NAL 与 PTS。
// videoPID/codec 在首段从 PAT/PMT 解析后会缓存在 tsScratch 里；未结束的 PES 跨分片续拼。
func accessUnitsFromTSChunk(seg []byte, sc *tsScratch) []accessUnit {
	if len(seg) < tsPacketSize {
		return nil
	}
	var out []accessUnit

	if sc.videoPID == 0 {
		if p, c := pmtVideoStream(seg); p != 0 {
			sc.videoPID = p
			sc.codec = c
		}
	}

	for off := 0; off+tsPacketSize <= len(seg); off += tsPacketSize {
		pkt := seg[off : off+tsPacketSize]
		// sync_byte 固定 0x47。
		if pkt[0] != 0x47 {
			continue
		}
		// PID：13 bit，来自 pkt[1] 低 5 位 + pkt[2]。
		pid := uint16(pkt[1]&0x1f)<<8 | uint16(pkt[2])
		if pid != sc.videoPID || sc.videoPID == 0 {
			continue
		}
		payloadStart := pkt[1]&0x40 != 0
		adapt := pkt[3] & 0x30
		idx := 4
		// adaptation_field_control：有适配域则跳过 adaptLen 字节。
		if adapt == 0x30 || adapt == 0x20 {
			if idx >= len(pkt) {
				continue
			}
			adaptLen := int(pkt[idx])
			idx++
			idx += adaptLen
		}
		if idx >= len(pkt) {
			continue
		}
		payload := pkt[idx:]
		if payloadStart {
			// 新 PES 开始：先把上一包 PES 收成 accessUnit。
			if sc.havePES && len(sc.pesBuf) > 0 {
				if au, ok := pesToAccessUnit(sc.pesBuf, sc.pesPTS, sc.codec); ok {
					out = append(out, au)
				}
			}
			sc.pesBuf = append([]byte(nil), payload...)
			sc.pesPTS = 0
			sc.havePES = true
			if pts, ok := pesHeaderPTS(sc.pesBuf); ok {
				sc.pesPTS = pts
			}
		} else if sc.havePES {
			sc.pesBuf = append(sc.pesBuf, payload...)
		}
	}
	return out
}

// flushTSPES 在分片轮换或关闭时把未结束的 PES 收成 accessUnit（若有）。
func flushTSPES(sc *tsScratch) (accessUnit, bool) {
	if !sc.havePES || len(sc.pesBuf) == 0 {
		sc.havePES = false
		sc.pesBuf = nil
		return accessUnit{}, false
	}
	au, ok := pesToAccessUnit(sc.pesBuf, sc.pesPTS, sc.codec)
	sc.havePES = false
	sc.pesBuf = nil
	return au, ok
}

// pmtVideoStream 扫描分段内 PAT→PMT，返回视频 elementary PID 与 stream_type 映射的 codec。
func pmtVideoStream(seg []byte) (uint16, flv.VideoKind) {
	var patPID uint16 = 0
	for off := 0; off+tsPacketSize <= len(seg); off += tsPacketSize {
		pkt := seg[off : off+tsPacketSize]
		if pkt[0] != 0x47 {
			continue
		}
		pid := uint16(pkt[1]&0x1f)<<8 | uint16(pkt[2])
		if pid != 0 {
			continue
		}
		if pkt[1]&0x40 == 0 {
			continue
		}
		payload := tsPayload(pkt)
		if len(payload) < 8 || payload[0] != 0 {
			continue
		}
		sectionLen := int(payload[1]&0x0f)<<8 | int(payload[2])
		if sectionLen+3 > len(payload) {
			continue
		}
		for i := 8; i+4 <= sectionLen+3-1 && i+4 <= len(payload); i += 4 {
			program := uint16(payload[i])<<8 | uint16(payload[i+1])
			pmt := uint16(payload[i+2]&0x1f)<<8 | uint16(payload[i+3])
			if program != 0 {
				patPID = pmt
			}
		}
		break
	}
	if patPID == 0 {
		return 0, flv.VideoKindNone
	}
	for off := 0; off+tsPacketSize <= len(seg); off += tsPacketSize {
		pkt := seg[off : off+tsPacketSize]
		if pkt[0] != 0x47 {
			continue
		}
		pid := uint16(pkt[1]&0x1f)<<8 | uint16(pkt[2])
		if pid != patPID || pkt[1]&0x40 == 0 {
			continue
		}
		payload := tsPayload(pkt)
		if len(payload) < 12 || payload[0] != 2 {
			continue
		}
		sectionLen := int(payload[1]&0x0f)<<8 | int(payload[2])
		progInfoLen := int(payload[10]&0x0f)<<8 | int(payload[11])
		i := 12 + progInfoLen
		end := sectionLen + 3 - 4
		for i+5 <= end && i+5 <= len(payload) {
			streamType := payload[i]
			elemPID := uint16(payload[i+1]&0x1f)<<8 | uint16(payload[i+2])
			esInfoLen := int(payload[i+3]&0x0f)<<8 | int(payload[i+4])
			switch streamType {
			case 0x1B:
				return elemPID, flv.VideoKindAVC
			case 0x24:
				return elemPID, flv.VideoKindHEVCEnhanced
			}
			i += 5 + esInfoLen
		}
		break
	}
	return 0, flv.VideoKindNone
}

// tsPayload 跳过 TS 头与 adaptation field，返回有效负载切片（仍指向原 pkt）。
func tsPayload(pkt []byte) []byte {
	idx := 4
	adapt := pkt[3] & 0x30
	if adapt == 0x30 || adapt == 0x20 {
		adaptLen := int(pkt[idx])
		idx++
		idx += adaptLen
	}
	if idx >= len(pkt) {
		return nil
	}
	return pkt[idx:]
}

// pesHeaderPTS 从 PES 头解析 PTS（90 kHz 时钟），需 PTS_DTS_flags 含 PTS。
func pesHeaderPTS(pes []byte) (uint64, bool) {
	if len(pes) < 9 || pes[0] != 0 || pes[1] != 0 || pes[2] != 1 {
		return 0, false
	}
	ptsDtsFlags := pes[7] >> 6
	if ptsDtsFlags&0x2 == 0 {
		return 0, false
	}
	headerLen := int(pes[8])
	if len(pes) < 9+headerLen {
		return 0, false
	}
	p := pes[9:]
	if ptsDtsFlags&0x2 != 0 {
		if len(p) < 5 {
			return 0, false
		}
		pts := uint64(p[0]>>1&0x07)<<30 |
			uint64(p[1])<<22 |
			uint64(p[2]>>1)<<15 |
			uint64(p[3])<<7 |
			uint64(p[4]>>1)
		return pts, true
	}
	return 0, false
}

// pesToAccessUnit 跳过 PES 头，剩余负载作为 Annex-B NAL；PTS 从 90kHz 换算为毫秒。
func pesToAccessUnit(pes []byte, pts90k uint64, codec flv.VideoKind) (accessUnit, bool) {
	if len(pes) < 9 {
		return accessUnit{}, false
	}
	headerLen := int(pes[8])
	if len(pes) < 9+headerLen {
		return accessUnit{}, false
	}
	nals := pes[9+headerLen:]
	if len(nals) == 0 {
		return accessUnit{}, false
	}
	ptsMs := int64(pts90k / 90)
	return accessUnit{PTSMs: ptsMs, Codec: codec, NALs: nals}, true
}
