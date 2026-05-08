package processors

import (
	"context"
	"encoding/binary"

	"github.com/eric2788/bilirec/pkg/pipeline"
	"github.com/sirupsen/logrus"
)

const tsPacketSize = 188
const tsSyncByte = 0x47

// TsContinuityFixerProcessor repairs MPEG-TS continuity counters at segment
// boundaries.
//
// Each MPEG-TS packet carries a 4-bit continuity counter (CC) per PID that
// must increment by 1 (mod 16) between consecutive packets of the same PID.
// When two independently-encoded HLS segments are concatenated, the CC of the
// first packet of the new segment may not follow on from the CC of the last
// packet of the previous segment.  Players tolerate small gaps but repeated
// discontinuities can cause A/V desync or decoder errors in strict decoders.
//
// This processor patches the CC values of every packet in each incoming
// segment so that they continue seamlessly from the previous segment.
//
// Note: packets with the discontinuity_indicator bit set (adaptation field
// bit 0x80) are exempt — their counter reset is intentional.
type TsContinuityFixerProcessor struct {
	// lastCC maps PID → last seen continuity counter value (0-15)
	lastCC map[uint16]uint8
}

func NewTsContinuityFixer() *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"ts-continuity-fixer",
		&TsContinuityFixerProcessor{},
	)
}

func (p *TsContinuityFixerProcessor) Open(_ context.Context, _ *logrus.Entry) error {
	p.lastCC = make(map[uint16]uint8)
	return nil
}

// Process rewrites continuity counters in place and returns the (modified) data.
func (p *TsContinuityFixerProcessor) Process(_ context.Context, log *logrus.Entry, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	patched := 0
	for off := 0; off+tsPacketSize <= len(data); off += tsPacketSize {
		pkt := data[off : off+tsPacketSize]
		if pkt[0] != tsSyncByte {
			// Not a valid TS packet start; skip rather than corrupt further.
			continue
		}

		header := binary.BigEndian.Uint32(pkt[0:4])
		pid := uint16((header >> 8) & 0x1FFF)
		adaptationFieldControl := (header >> 4) & 0x3
		cc := uint8(header & 0xF)

		// Packets with no payload (adaptation_field_control == 0b10) do not
		// increment the CC; skip them.
		if adaptationFieldControl == 0x2 {
			continue
		}

		// Check discontinuity_indicator in the adaptation field header.
		if adaptationFieldControl == 0x3 {
			if len(pkt) > 5 && pkt[4] > 0 { // adaptation field length > 0
				if pkt[5]&0x80 != 0 { // discontinuity_indicator set
					// Intentional discontinuity — update lastCC but don't patch.
					p.lastCC[pid] = cc
					continue
				}
			}
		}

		expected, seen := p.lastCC[pid]
		if seen {
			want := (expected + 1) & 0xF
			if cc != want {
				log.Debugf("ts-continuity-fixer: PID %d: CC %d→%d", pid, cc, want)
				// Rewrite the CC nibble in byte 3 of the TS header.
				pkt[3] = (pkt[3] & 0xF0) | want
				cc = want
				patched++
			}
		}
		p.lastCC[pid] = cc
	}

	if patched > 0 {
		log.Debugf("ts-continuity-fixer: patched %d packets in segment (%d B)", patched, len(data))
	}
	return data, nil
}

func (p *TsContinuityFixerProcessor) Close() error { return nil }
