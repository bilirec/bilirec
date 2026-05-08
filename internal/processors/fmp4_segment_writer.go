package processors

import (
	"context"
	"encoding/binary"
	"os"
	"sync"
	"time"

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
	mu     sync.Mutex
	path   string
	file   *os.File
	logger *logrus.Entry
	syncer *periodicFileSync
	bases  *map[uint32]uint64
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

func readBoxHeader(data []byte, offset int) (size int, boxType string, headerLen int, ok bool) {
	if offset < 0 || offset+8 > len(data) {
		return 0, "", 0, false
	}
	size32 := binary.BigEndian.Uint32(data[offset : offset+4])
	boxType = string(data[offset+4 : offset+8])
	headerLen = 8

	switch size32 {
	case 0:
		size = len(data) - offset
	case 1:
		if offset+16 > len(data) {
			return 0, "", 0, false
		}
		size64 := binary.BigEndian.Uint64(data[offset+8 : offset+16])
		if size64 > uint64(len(data)-offset) {
			return 0, "", 0, false
		}
		headerLen = 16
		size = int(size64)
	default:
		size = int(size32)
	}

	if size < headerLen || offset+size > len(data) {
		return 0, "", 0, false
	}
	return size, boxType, headerLen, true
}

func tfhdTrackID(data []byte, start, end int) (uint32, bool) {
	if end-start < 16 {
		return 0, false
	}
	// FullBox(4 bytes version+flags) + track_ID(4 bytes)
	trackID := binary.BigEndian.Uint32(data[start+12 : start+16])
	if trackID == 0 {
		return 0, false
	}
	return trackID, true
}

func normalizeTfdtBox(data []byte, start, end int, trackID uint32, bases map[uint32]uint64) bool {
	if trackID == 0 || end-start < 16 {
		return false
	}
	version := data[start+8]

	if version == 1 {
		if end-start < 20 {
			return false
		}
		value := binary.BigEndian.Uint64(data[start+12 : start+20])
		base, exists := bases[trackID]
		if !exists || value < base {
			bases[trackID] = value
			base = value
		}
		binary.BigEndian.PutUint64(data[start+12:start+20], value-base)
		return true
	}

	value := uint64(binary.BigEndian.Uint32(data[start+12 : start+16]))
	base, exists := bases[trackID]
	if !exists || value < base {
		bases[trackID] = value
		base = value
	}
	binary.BigEndian.PutUint32(data[start+12:start+16], uint32(value-base))
	return true
}

func normalizeTrafTfdt(data []byte, trafStart, trafEnd int, bases map[uint32]uint64) int {
	trackID := uint32(0)

	for off := trafStart + 8; off < trafEnd; {
		size, typ, _, ok := readBoxHeader(data, off)
		if !ok {
			break
		}
		boxEnd := off + size
		if typ == "tfhd" {
			if id, found := tfhdTrackID(data, off, boxEnd); found {
				trackID = id
				break
			}
		}
		off = boxEnd
	}

	if trackID == 0 {
		return 0
	}

	normalized := 0
	for off := trafStart + 8; off < trafEnd; {
		size, typ, _, ok := readBoxHeader(data, off)
		if !ok {
			break
		}
		boxEnd := off + size
		if typ == "tfdt" && normalizeTfdtBox(data, off, boxEnd, trackID, bases) {
			normalized++
		}
		off = boxEnd
	}

	return normalized
}

func normalizeFragmentTimestamps(data []byte, bases map[uint32]uint64) int {
	total := 0
	for off := 0; off < len(data); {
		size, typ, _, ok := readBoxHeader(data, off)
		if !ok {
			break
		}
		boxEnd := off + size

		if typ == "moof" {
			for child := off + 8; child < boxEnd; {
				childSize, childType, _, childOK := readBoxHeader(data, child)
				if !childOK {
					break
				}
				childEnd := child + childSize
				if childType == "traf" {
					total += normalizeTrafTfdt(data, child, childEnd, bases)
				}
				child = childEnd
			}
		}

		off = boxEnd
	}
	return total
}

func (p *Fmp4SegmentWriterProcessor) Process(ctx context.Context, log *logrus.Entry, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(data) >= 8 {
		boxType := string(data[4:8])
		log.Debugf("fmp4 segment: box=%s size=%d", boxType, len(data))
	}
	if normalized := normalizeFragmentTimestamps(data, *p.bases); normalized > 0 {
		log.Debugf("fmp4 segment: normalized tfdt boxes=%d", normalized)
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
