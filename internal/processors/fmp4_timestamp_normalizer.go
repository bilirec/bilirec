package processors

import (
	"context"

	"github.com/bilirec/bilirec/pkg/logger"

	"github.com/bilirec/bilirec/pkg/hls"
	"github.com/bilirec/bilirec/pkg/pipeline"
)

// Fmp4TimestampNormalizerProcessor normalizes tfdt timestamps in fragmented MP4
// segments in place so concatenated segments keep monotonic decode timelines.
type Fmp4TimestampNormalizerProcessor struct {
	bases                  *map[uint32]uint64
	segmentCount           uint64
	normalizedSegmentCount uint64
	normalizedTfdtTotal    uint64
}

func NewFmp4TimestampNormalizer(bases *map[uint32]uint64) *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"fmp4-timestamp-normalizer",
		&Fmp4TimestampNormalizerProcessor{bases: bases},
	)
}

func (p *Fmp4TimestampNormalizerProcessor) Open(_ context.Context, _ logger.Logger) error {
	p.segmentCount = 0
	p.normalizedSegmentCount = 0
	p.normalizedTfdtTotal = 0
	return nil
}

func (p *Fmp4TimestampNormalizerProcessor) Process(_ context.Context, log logger.Logger, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	p.segmentCount++
	segmentNo := p.segmentCount

	if log.Enabled(logger.DebugLevel) && len(data) >= 8 {
		boxType := string(data[4:8])
		if segmentNo <= 3 || boxType == "ftyp" || segmentNo%60 == 0 {
			log.Debugf("fmp4 segment: box=%s size=%d seg=%d", boxType, len(data), segmentNo)
		}
	}

	if normalized := hls.NormalizeFragmentTimestamps(data, *p.bases); normalized > 0 {
		p.normalizedSegmentCount++
		p.normalizedTfdtTotal += uint64(normalized)
		if segmentNo <= 3 || normalized >= 16 {
			log.Debugf("fmp4 segment: normalized tfdt boxes=%d seg=%d", normalized, segmentNo)
		} else if segmentNo%60 == 0 {
			avg := float64(p.normalizedTfdtTotal) / float64(p.normalizedSegmentCount)
			log.Debugf("fmp4 segment: normalized tfdt summary seg=%d normalized-segments=%d total-boxes=%d avg=%.2f", segmentNo, p.normalizedSegmentCount, p.normalizedTfdtTotal, avg)
		}
	} else if segmentNo%60 == 0 {
		log.Debugf("fmp4 segment: normalized tfdt summary seg=%d normalized-segments=%d total-boxes=%d", segmentNo, p.normalizedSegmentCount, p.normalizedTfdtTotal)
	}

	return data, nil
}

func (p *Fmp4TimestampNormalizerProcessor) Close() error { return nil }
