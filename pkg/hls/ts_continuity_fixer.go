package hls

import "encoding/binary"

const (
	tsPacketSize = 188
	tsSyncByte   = 0x47
)

// TsContinuityFixer repairs MPEG-TS continuity counters at segment boundaries.
//
// The fixer keeps PID-local CC state across segments and patches incoming
// packets in place so each payload-bearing packet continues from the previous
// one modulo 16.
type TsContinuityFixer struct {
	lastCC [8192]uint8
	seen   [8192]bool
}

type TsContinuityFixResult struct {
	Patched int
}

func NewTsContinuityFixer() *TsContinuityFixer {
	f := &TsContinuityFixer{}
	f.Reset()
	return f
}

func (f *TsContinuityFixer) Reset() {
	for i := range f.seen {
		f.seen[i] = false
	}
}

// FixSegment rewrites continuity counters in place and returns patch stats.
func (f *TsContinuityFixer) FixSegment(data []byte) TsContinuityFixResult {
	if len(data) == 0 {
		return TsContinuityFixResult{}
	}

	patched := 0
	for off := 0; off+tsPacketSize <= len(data); off += tsPacketSize {
		pkt := data[off : off+tsPacketSize]
		if pkt[0] != tsSyncByte {
			continue
		}

		header := binary.BigEndian.Uint32(pkt[0:4])
		pid := uint16((header >> 8) & 0x1FFF)
		adaptationFieldControl := (header >> 4) & 0x3
		cc := uint8(header & 0xF)

		// Only payload-bearing packets can advance CC.
		if adaptationFieldControl == 0x0 || adaptationFieldControl == 0x2 {
			continue
		}

		// If discontinuity_indicator is set, keep the observed CC as baseline.
		if adaptationFieldControl == 0x3 {
			if len(pkt) > 5 && pkt[4] > 0 {
				if pkt[5]&0x80 != 0 {
					f.lastCC[pid] = cc
					f.seen[pid] = true
					continue
				}
			}
		}

		expected, seen := f.lastCC[pid], f.seen[pid]
		if seen {
			want := (expected + 1) & 0xF
			if cc != want {
				pkt[3] = (pkt[3] & 0xF0) | want
				cc = want
				patched++
			}
		}
		f.lastCC[pid] = cc
		f.seen[pid] = true
	}

	return TsContinuityFixResult{Patched: patched}
}
