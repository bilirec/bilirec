package sei

func isAnnexB(data []byte) bool {
	if len(data) < 3 {
		return false
	}
	if data[0] == 0 && data[1] == 0 && data[2] == 1 {
		return true
	}
	return len(data) >= 4 && data[0] == 0 && data[1] == 0 && data[2] == 0 && data[3] == 1
}

func foreachAVCC(data []byte, codec Codec, fn func(rbsp []byte)) {
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

func foreachAnnexB(data []byte, codec Codec, fn func(rbsp []byte)) {
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

func seiRBSPFromNAL(nal []byte, codec Codec) []byte {
	if len(nal) == 0 {
		return nil
	}
	switch codec {
	case AVC:
		if nal[0]&0x1f != 6 {
			return nil
		}
		return nal[1:]
	case HEVC:
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
