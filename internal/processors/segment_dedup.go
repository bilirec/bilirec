package processors

import (
	"context"
	"crypto/sha256"

	"github.com/bilirec/bilirec/pkg/pipeline"
	"github.com/sirupsen/logrus"
)

// SegmentDedupProcessor drops a segment whose SHA-256 matches the previous
// segment.  This handles the case where the HLS poller re-downloads the same
// sequence number before the playlist advances, producing an exact byte-for-byte
// duplicate in the output file.
//
// Only consecutive duplicates are caught; non-consecutive repeats are allowed
// (e.g. a legitimate loop in very short test streams).
//
// Two-phase approach:
//   - Phase 1: quick fingerprint (length + first 8 + last 8 bytes). Unique
//     segments almost always differ here; SHA-256 is skipped entirely.
//   - Phase 2: full SHA-256, only reached when the fingerprint matches
//     (indicating a likely consecutive duplicate).
type SegmentDedupProcessor struct {
	lastLen  int
	lastHead [8]byte
	lastTail [8]byte
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
	*p = SegmentDedupProcessor{}
	return nil
}

// fingerprintMatches returns true when length and boundary bytes of data match
// the stored fingerprint, making a full SHA-256 check worthwhile.
func (p *SegmentDedupProcessor) fingerprintMatches(data []byte) bool {
	n := len(data)
	if n != p.lastLen {
		return false
	}
	headLen := min(n, 8)
	for i := 0; i < headLen; i++ {
		if data[i] != p.lastHead[i] {
			return false
		}
	}
	tailLen := min(n, 8)
	for i := 0; i < tailLen; i++ {
		if data[n-tailLen+i] != p.lastTail[i] {
			return false
		}
	}
	return true
}

// updateFingerprint stores the length and boundary bytes of data.
func (p *SegmentDedupProcessor) updateFingerprint(data []byte) {
	n := len(data)
	p.lastLen = n
	headLen := min(n, 8)
	copy(p.lastHead[:], data[:headLen])
	tailLen := min(n, 8)
	copy(p.lastTail[:], data[n-tailLen:])
	p.hasLast = true
}

// Process returns (nil, nil) for a duplicate segment, signalling downstream
// processors (writers) to skip writing.
func (p *SegmentDedupProcessor) Process(_ context.Context, log *logrus.Entry, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	// Phase 1: quick fingerprint — skip SHA-256 for the common case where
	// consecutive segments differ in length or boundary bytes.
	if p.hasLast && !p.fingerprintMatches(data) {
		p.updateFingerprint(data)
		return data, nil
	}

	// Phase 2: full SHA-256 — only reached when fingerprint matches.
	sum := sha256.Sum256(data)
	if p.hasLast && sum == p.lastHash {
		log.Warnf("segment-dedup：丢弃重复分片（%d B）", len(data))
		return nil, nil
	}
	p.lastHash = sum
	p.updateFingerprint(data)
	return data, nil
}

func (p *SegmentDedupProcessor) Close() error { return nil }
