package processors

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/eric2788/bilirec/pkg/hls"
	"github.com/eric2788/bilirec/pkg/pipeline"
	"github.com/sirupsen/logrus"
)

// Fmp4SegmentWriterProcessor appends complete HLS fMP4 segments to a single
// fragmented MP4 source file (typically using the .fmp4 extension). Each
// Process() call receives one full segment:
//   - Init segment:  bytes 4-8 == "ftyp"  (ftyp+moov boxes)
//   - Media segment: bytes 4-8 == "moof"  (moof+mdat boxes)
//
// Appending init then media segments produces a valid fragmented MP4 source
// file. It is directly playable, but remuxing to a final .mp4 still improves
// seek behaviour because ffmpeg rebuilds the container metadata for random
// access.
type Fmp4SegmentWriterProcessor struct {
	mu                     sync.Mutex
	path                   string
	file                   *os.File
	logger                 *logrus.Entry
	syncer                 *periodicFileSync
	bases                  *map[uint32]uint64
	segmentCount           uint64
	normalizedSegmentCount uint64
	normalizedTfdtTotal    uint64
}

func NewFmp4SegmentWriter(path string, bases *map[uint32]uint64) *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"fmp4-segment-writer",
		&Fmp4SegmentWriterProcessor{path: path, bases: bases},
	)
}

func (p *Fmp4SegmentWriterProcessor) Open(ctx context.Context, log *logrus.Entry) error {
	f, err := os.OpenFile(p.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	p.file = f
	p.logger = log.WithField("file", f.Name())
	p.syncer = startPeriodicFileSync(&p.mu, p.file, p.logger, 30*time.Second)
	return nil
}

func (p *Fmp4SegmentWriterProcessor) Process(ctx context.Context, log *logrus.Entry, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.segmentCount++
	segmentNo := p.segmentCount
	if len(data) >= 8 {
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
	_, err := p.file.Write(data)
	return data, err
}

func (p *Fmp4SegmentWriterProcessor) Close() error {
	p.syncer.Stop()
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.file.Sync(); err != nil {
		p.logger.Warnf("error syncing file: %v", err)
	}
	return p.file.Close()
}
