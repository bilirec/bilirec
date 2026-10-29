package processors

import (
	"context"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"

	"github.com/bilirec/bilirec/pkg/hls"
	"github.com/bilirec/bilirec/pkg/pipeline"
)

// TimelineGap is a positive media timeline discontinuity reported to observers.
type TimelineGap struct {
	Delta time.Duration
}

// Fmp4TimestampNormalizerProcessor normalizes tfdt timestamps in fragmented MP4
// segments in place so concatenated segments keep monotonic decode timelines.
type Fmp4TimestampNormalizerProcessor struct {
	bases                  *map[uint32]uint64
	lastRawTfdt            map[uint32]uint64
	timescales             map[uint32]uint32
	onGap                  func(TimelineGap)
	segmentCount           uint64
	normalizedSegmentCount uint64
	normalizedTfdtTotal    uint64
}

func NewFmp4TimestampNormalizer(bases *map[uint32]uint64, onGap func(TimelineGap)) *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"fmp4-timestamp-normalizer",
		&Fmp4TimestampNormalizerProcessor{bases: bases, onGap: onGap},
	)
}

func (p *Fmp4TimestampNormalizerProcessor) Open(_ context.Context, _ logger.Logger) error {
	p.segmentCount = 0
	p.normalizedSegmentCount = 0
	p.normalizedTfdtTotal = 0
	p.lastRawTfdt = make(map[uint32]uint64)
	p.timescales = nil
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

	// TrackTimescalesFromInit is a cheap top-level box walk that returns nil for
	// media-only fragments, so the learned map survives without an extra guard.
	if ts := hls.TrackTimescalesFromInit(data); len(ts) > 0 {
		p.timescales = ts
	}

	if gap := hls.NoteFragmentTimeline(data, p.timescales, p.lastRawTfdt); gap > 0 {
		log.Warnf("fmp4：检测到媒体时间轴跳变（delta=%v，seg=%d）", gap, segmentNo)
		if p.onGap != nil {
			p.onGap(TimelineGap{Delta: gap})
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
