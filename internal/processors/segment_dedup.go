package processors

import (
	"context"
	"crypto/sha256"

	"github.com/eric2788/bilirec/pkg/pipeline"
	"github.com/sirupsen/logrus"
)

// SegmentDedupProcessor drops a segment whose SHA-256 matches the previous
// segment.  This handles the case where the HLS poller re-downloads the same
// sequence number before the playlist advances, producing an exact byte-for-byte
// duplicate in the output file.
//
// Only consecutive duplicates are caught; non-consecutive repeats are allowed
// (e.g. a legitimate loop in very short test streams).
type SegmentDedupProcessor struct {
	lastHash [32]byte
	hasLast  bool
}

func NewSegmentDedup() *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"segment-dedup",
		&SegmentDedupProcessor{},
	)
}

func (p *SegmentDedupProcessor) Open(_ context.Context, _ *logrus.Entry) error {
	p.lastHash = [32]byte{}
	p.hasLast = false
	return nil
}

// Process returns (nil, nil) for a duplicate segment, signalling downstream
// processors (writers) to skip writing.
func (p *SegmentDedupProcessor) Process(_ context.Context, log *logrus.Entry, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}
	sum := sha256.Sum256(data)
	if p.hasLast && sum == p.lastHash {
		log.Warnf("segment-dedup: dropping duplicate segment (%d B)", len(data))
		return nil, nil
	}
	p.lastHash = sum
	p.hasLast = true
	return data, nil
}

func (p *SegmentDedupProcessor) Close() error { return nil }
