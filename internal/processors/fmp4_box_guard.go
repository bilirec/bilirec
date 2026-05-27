package processors

import (
	"context"

	"github.com/bilirec/bilirec/pkg/hls"
	"github.com/bilirec/bilirec/pkg/pipeline"
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
// "styp" is treated as a media-fragment prefix (styp+moof+mdat) and does NOT
// trigger a discontinuity reset — only "ftyp" does.
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
		log.Warnf("fmp4-box-guard：分片过短（%d B），已丢弃", len(data))
		return nil, nil
	}

	boxType := string(data[4:8])
	switch boxType {
	case "ftyp":
		// Init segment — if we already saw media, it's a discontinuity
		if p.seenMedia {
			log.Warnf("fmp4-box-guard：媒体分片后出现新的 init 分片，检测到流不连续，正在重置")
			*p.bases = make(map[uint32]uint64)
		}
	case "styp", "moof":
		// styp is a media-fragment prefix (styp+moof+mdat); moof is a plain
		// media fragment. Both indicate media data — mark seenMedia but do
		// NOT treat as an init/discontinuity.
		p.seenMedia = true
	default:
		log.Warnf("fmp4-box-guard：意外的前导 box %q（%d B），已丢弃", boxType, len(data))
		return nil, nil
	}

	// Validate that the declared box size doesn't exceed the segment buffer.
	_, _, _, ok := hls.ReadBoxHeader(data, 0)
	if !ok {
		log.Warnf("fmp4-box-guard：box 头格式错误（size 超出分片缓冲区），已丢弃")
		return nil, nil
	}

	return data, nil
}

func (p *Fmp4BoxGuardProcessor) Close() error { return nil }
