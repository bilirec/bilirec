package processors

import (
	"context"
	"fmt"

	"github.com/eric2788/bilirec/pkg/pipeline"
	"github.com/sirupsen/logrus"
)

// Fmp4BoxGuardProcessor validates that each fMP4 segment starts with a known
// ISO BMFF box type before passing it downstream.
//
// Valid leading box types:
//   - "ftyp" — init segment (ftyp + moov)
//   - "moof" — media fragment (moof + mdat)
//   - "styp" — segment type box (some encoders emit before moof)
//
// Segments with an unrecognised or truncated leading box are logged and
// dropped (nil returned), preventing corrupted data from reaching the writer.
//
// Additionally, when a new init segment ("ftyp") arrives after at least one
// media segment has been written, the processor signals a discontinuity by
// resetting the shared tfdt bases map via the pointer passed at construction.
type Fmp4BoxGuardProcessor struct {
	seenMedia bool
	bases     *map[uint32]uint64
}

func NewFmp4BoxGuard(bases *map[uint32]uint64) *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"fmp4-box-guard",
		&Fmp4BoxGuardProcessor{bases: bases},
	)
}

func (p *Fmp4BoxGuardProcessor) Open(_ context.Context, _ *logrus.Entry) error {
	p.seenMedia = false
	return nil
}

func (p *Fmp4BoxGuardProcessor) Process(_ context.Context, log *logrus.Entry, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}
	if len(data) < 8 {
		log.Warnf("fmp4-box-guard: segment too short (%d B), dropping", len(data))
		return nil, nil
	}

	boxType := string(data[4:8])
	switch boxType {
	case "ftyp", "styp":
		// Init segment — if we already saw media, it's a discontinuity
		if p.seenMedia {
			log.Warnf("fmp4-box-guard: new init segment after media — stream discontinuity, resetting")
			*p.bases = make(map[uint32]uint64)
		}
	case "moof":
		p.seenMedia = true
	default:
		log.Warnf("fmp4-box-guard: unexpected leading box %q (%d B), dropping", boxType, len(data))
		return nil, nil
	}

	// Validate that the declared box size doesn't exceed the segment buffer.
	_, _, _, ok := readBoxHeader(data, 0)
	if !ok {
		log.Warnf("fmp4-box-guard: malformed box header (size overflows segment buffer), dropping")
		return nil, fmt.Errorf("fmp4-box-guard: malformed box header")
	}

	return data, nil
}

func (p *Fmp4BoxGuardProcessor) Close() error { return nil }
