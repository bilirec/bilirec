package processors

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

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
	lastHash string
}

func NewSegmentDedup() *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"segment-dedup",
		&SegmentDedupProcessor{},
	)
}

func (p *SegmentDedupProcessor) Open(_ context.Context, _ *logrus.Entry) error {
	p.lastHash = ""
	return nil
}

// Process returns (nil, nil) for a duplicate segment, signalling downstream
// processors (writers) to skip writing.
func (p *SegmentDedupProcessor) Process(_ context.Context, log *logrus.Entry, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:8]) // first 8 bytes of SHA-256 — enough for dedup
	if h == p.lastHash {
		log.Warnf("segment-dedup: dropping duplicate segment (%d B)", len(data))
		return nil, nil
	}
	p.lastHash = h
	return data, nil
}

func (p *SegmentDedupProcessor) Close() error { return nil }
