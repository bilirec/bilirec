package processors

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/bilirec/bilirec/pkg/hls"
	"github.com/bilirec/bilirec/pkg/pipeline"
	"github.com/sirupsen/logrus"
)

// ErrFmp4Discontinuity is returned when a new init segment (ftyp) is detected
// after media segments have been processed, indicating a stream discontinuity.
// This error signals the strategy to rotate to a new output file.
var ErrFmp4Discontinuity = errors.New("fmp4: stream discontinuity detected, new init segment after media")

// Fmp4DiscontinuityError is a rich error returned when init segment content
// changes after media has been written. InitSegment carries the new init bytes
// for replay into the next output file.
type Fmp4DiscontinuityError struct {
	InitSegment []byte
}

func (e *Fmp4DiscontinuityError) Error() string {
	return fmt.Sprintf("%v (init %d B)", ErrFmp4Discontinuity, len(e.InitSegment))
}

func (e *Fmp4DiscontinuityError) Is(target error) bool {
	return target == ErrFmp4Discontinuity
}

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
// When a new init segment ("ftyp") arrives after media:
//   - If bytes match the last seen init, the segment is skipped (playlist re-send).
//   - If init content changed, Fmp4DiscontinuityError is returned to rotate files.
//
// "styp" is treated as a media-fragment prefix (styp+moof+mdat) and does NOT
// trigger a discontinuity reset — only "ftyp" does.
//
// MapURI / init churn is settled upstream in the HLS reader (compare + debounce);
// this processor remains the last-line rotate signal on a changed ftyp.
type Fmp4BoxGuardProcessor struct {
	seenMedia bool
	bases     *map[uint32]uint64
	lastInit  *[]byte
}

func NewFmp4BoxGuard(bases *map[uint32]uint64, lastInit *[]byte) *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"fmp4-box-guard",
		&Fmp4BoxGuardProcessor{bases: bases, lastInit: lastInit},
	)
}

func (p *Fmp4BoxGuardProcessor) Open(_ context.Context, _ *logrus.Entry) error {
	p.seenMedia = false
	return nil
}

func (p *Fmp4BoxGuardProcessor) storeLastInit(data []byte) {
	if p.lastInit == nil {
		return
	}
	*p.lastInit = append((*p.lastInit)[:0], data...)
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
		if p.seenMedia {
			if p.lastInit != nil && len(*p.lastInit) > 0 && bytes.Equal(data, *p.lastInit) {
				log.Debugf("fmp4-box-guard：媒体分片后收到相同 init 分片（%d B），已跳过", len(data))
				return nil, nil
			}
			log.Warnf("fmp4-box-guard：媒体分片后出现新的 init 分片（%d B），检测到流不连续，需要切换文件", len(data))
			p.storeLastInit(data)
			return nil, &Fmp4DiscontinuityError{InitSegment: append([]byte(nil), data...)}
		}
		p.storeLastInit(data)
	case "styp", "moof":
		p.seenMedia = true
	default:
		log.Warnf("fmp4-box-guard：意外的前导 box %q（%d B），已丢弃", boxType, len(data))
		return nil, nil
	}

	_, _, _, ok := hls.ReadBoxHeader(data, 0)
	if !ok {
		log.Warnf("fmp4-box-guard：box 头格式错误（size 超出分片缓冲区），已丢弃")
		return nil, nil
	}

	return data, nil
}

func (p *Fmp4BoxGuardProcessor) Close() error { return nil }
