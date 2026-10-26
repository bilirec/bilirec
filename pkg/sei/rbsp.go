package sei

func userDataUnregisteredFromRBSP(rbsp []byte) [][]byte {
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
		if em[i] == 0x80 {
			break
		}
	}
	return out
}

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
