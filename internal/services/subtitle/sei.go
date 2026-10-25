package subtitle

import (
	"github.com/bilirec/bilirec/pkg/flv"
)

const maxSEIRBSPBytes = 64 * 1024

// foreachSEIRBSP 遍历 accessUnit 中的 SEI NAL，对每个 NAL 的 RBSP 切片调用 fn（切片仍指向录制缓冲，fn 内须尽快拷贝）。
func foreachSEIRBSP(au accessUnit, fn func(rbsp []byte)) {
	if len(au.NALs) == 0 {
		return
	}
	if isAnnexB(au.NALs) {
		foreachSEIRBSPAnnexB(au.NALs, au.Codec, fn)
		return
	}
	foreachSEIRBSPAVCC(au.NALs, au.Codec, fn)
}

func foreachSEIRBSPAVCC(data []byte, codec flv.VideoKind, fn func(rbsp []byte)) {
	rest := data
	for len(rest) >= 4 {
		n := int(uint32(rest[0])<<24 | uint32(rest[1])<<16 | uint32(rest[2])<<8 | uint32(rest[3]))
		rest = rest[4:]
		if n <= 0 || n > len(rest) {
			break
		}
		nal := rest[:n]
		rest = rest[n:]
		if rbsp := seiRBSPFromNAL(nal, codec); len(rbsp) > 0 {
			fn(rbsp)
		}
	}
}

func foreachSEIRBSPAnnexB(data []byte, codec flv.VideoKind, fn func(rbsp []byte)) {
	i := 0
	for i < len(data) {
		start := findStartCode(data, i)
		if start < 0 {
			break
		}
		scLen := startCodeLen(data, start)
		if scLen == 0 {
			i = start + 1
			continue
		}
		i = start + scLen
		next := findStartCode(data, i)
		end := len(data)
		if next >= 0 {
			end = next
		}
		if i < end {
			nal := data[i:end]
			if rbsp := seiRBSPFromNAL(nal, codec); len(rbsp) > 0 {
				fn(rbsp)
			}
		}
		i = end
	}
}

func seiRBSPFromNAL(nal []byte, codec flv.VideoKind) []byte {
	if len(nal) == 0 {
		return nil
	}
	switch codec {
	case flv.VideoKindAVC:
		if nal[0]&0x1f != 6 {
			return nil
		}
		return nal[1:]
	case flv.VideoKindHEVCLegacy, flv.VideoKindHEVCEnhanced:
		if len(nal) < 2 {
			return nil
		}
		nalType := (nal[0] & 0x7E) >> 1
		if nalType != 39 && nalType != 40 {
			return nil
		}
		return nal[2:]
	default:
		return nil
	}
}

// seiPayloads 从 accessUnit.NALs 里找出所有 SEI 消息体（H.264 user_data_unregistered 等）。
func seiPayloads(au accessUnit) [][]byte {
	if len(au.NALs) == 0 {
		return nil
	}
	if isAnnexB(au.NALs) {
		return seiFromAnnexB(au.NALs, au.Codec)
	}
	return seiFromAVCC(au.NALs, au.Codec)
}

// isAnnexB 区分 AVCC 与 Annex-B：前者以 4 字节长度开头，后者以 00 00 01 起始码开头。
func isAnnexB(data []byte) bool {
	if len(data) < 3 {
		return false
	}
	if data[0] == 0 && data[1] == 0 && data[2] == 1 {
		return true
	}
	return len(data) >= 4 && data[0] == 0 && data[1] == 0 && data[2] == 0 && data[3] == 1
}

// seiFromAVCC 按 [4B 大端长度][NAL]… 遍历，在每个 NAL 内解析 SEI RBSP。
func seiFromAVCC(data []byte, codec flv.VideoKind) [][]byte {
	var out [][]byte
	rest := data
	for len(rest) >= 4 {
		n := int(uint32(rest[0])<<24 | uint32(rest[1])<<16 | uint32(rest[2])<<8 | uint32(rest[3]))
		rest = rest[4:]
		if n <= 0 || n > len(rest) {
			break
		}
		nal := rest[:n]
		rest = rest[n:]
		for _, p := range seiPayloadsInNAL(nal, codec) {
			out = append(out, p)
		}
	}
	return out
}

// seiFromAnnexB 按起始码切 NAL，再对每个 NAL 解析 SEI。
func seiFromAnnexB(data []byte, codec flv.VideoKind) [][]byte {
	var out [][]byte
	i := 0
	for i < len(data) {
		start := findStartCode(data, i)
		if start < 0 {
			break
		}
		scLen := startCodeLen(data, start)
		if scLen == 0 {
			i = start + 1
			continue
		}
		i = start + scLen
		next := findStartCode(data, i)
		end := len(data)
		if next >= 0 {
			end = next
		}
		if i < end {
			nal := data[i:end]
			for _, p := range seiPayloadsInNAL(nal, codec) {
				out = append(out, p)
			}
		}
		i = end
	}
	return out
}

func findStartCode(b []byte, from int) int {
	for i := from; i+3 <= len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 {
			if b[i+2] == 1 {
				return i
			}
			if i+3 < len(b) && b[i+2] == 0 && b[i+3] == 1 {
				return i
			}
		}
	}
	return -1
}

func startCodeLen(b []byte, at int) int {
	if at+2 < len(b) && b[at] == 0 && b[at+1] == 0 && b[at+2] == 1 {
		return 3
	}
	if at+3 < len(b) && b[at] == 0 && b[at+1] == 0 && b[at+2] == 0 && b[at+3] == 1 {
		return 4
	}
	return 0
}

// seiPayloadsInNAL 只处理 SEI NAL（H.264 type 6；HEVC type 39/40），返回 RBSP 内各条 payload 原始字节。
func seiPayloadsInNAL(nal []byte, codec flv.VideoKind) [][]byte {
	if len(nal) == 0 {
		return nil
	}
	switch codec {
	case flv.VideoKindAVC:
		if nal[0]&0x1f != 6 {
			return nil
		}
		return parseSEIRBSP(nal[1:])
	case flv.VideoKindHEVCLegacy, flv.VideoKindHEVCEnhanced:
		if len(nal) < 2 {
			return nil
		}
		nalType := (nal[0] & 0x7E) >> 1
		if nalType != 39 && nalType != 40 {
			return nil
		}
		return parseSEIRBSP(nal[2:])
	default:
		return nil
	}
}

// parseSEIRBSP 解析 SEI RBSP：多条 (payloadType, payloadSize, payload) 串联；type 5 且长度≥16 为 user_data_unregistered。
// B 站 AI 字幕在 type 5 payload 内：16 字节 UUID "BILILIVESUBTITLE" + JSON。
func parseSEIRBSP(rbsp []byte) [][]byte {
	em := unescapeRBSP(rbsp)
	var out [][]byte
	i := 0
	for i < len(em) {
		pt, n := readSEIValue(em, i)
		if n == 0 {
			break
		}
		i += n
		ps, n := readSEIValue(em, i)
		if n == 0 {
			break
		}
		i += n
		if i+ps > len(em) {
			break
		}
		payload := em[i : i+ps]
		i += ps
		if pt == 5 && ps >= 16 {
			out = append(out, payload)
		}
		if i >= len(em) {
			break
		}
		// rbsp_trailing_bits：下一个字节为 0x80 表示 RBSP 结束。
		if em[i] == 0x80 {
			break
		}
	}
	return out
}

// readSEIValue 读 H.264/HEVC 的 ue(v) 风格可变长整数（0xFF 表示继续累加）。
func readSEIValue(b []byte, off int) (int, int) {
	if off >= len(b) {
		return 0, 0
	}
	v := 0
	n := 0
	for {
		if off+n >= len(b) {
			return 0, 0
		}
		byteVal := int(b[off+n])
		n++
		v += byteVal
		if byteVal != 0xFF {
			break
		}
	}
	return v, n
}

// unescapeRBSP 去掉 emulation prevention：码流里的 0x00 0x00 0x03 → 0x00 0x00。
func unescapeRBSP(rbsp []byte) []byte {
	if len(rbsp) == 0 {
		return rbsp
	}
	out := make([]byte, 0, len(rbsp))
	for i := 0; i < len(rbsp); i++ {
		if i+2 < len(rbsp) && rbsp[i] == 0 && rbsp[i+1] == 0 && rbsp[i+2] == 3 {
			out = append(out, 0, 0)
			i += 2
			continue
		}
		out = append(out, rbsp[i])
	}
	return out
}
