package flv

import (
	"bytes"
	"sync"

	"github.com/bilirec/bilirec/pkg/pool"
)

// =====================================================
// REALTIME FIXER - 逐個 Tag 修復並輸出
// =====================================================

type RealtimeFixer struct {
	mu                sync.Mutex
	tsStore           *TimestampStore
	buffer            *bytes.Buffer
	bufferPool        *pool.BufferPool
	ownBufferPool     bool
	maxBufferSize     int
	maxTagDataSize    int
	outputBuf         bytes.Buffer // reused across Fix calls (scheme B)
	sourceHeaderOK    bool
	isRotationSegment bool // set by ResetTimestampStore; rotation segments have no FLV file header
	dedupCache        *DedupCache
	dupCount          int64
	lastDedupClean    int32 // timestamp of last dedup clean
	fixCalls          uint32
	jumpReporter      TimestampJumpReporter
}

func NewRealtimeFixer(opts ...RealtimeFixerOption) *RealtimeFixer {
	cfg := applyRealtimeFixerOptions(opts...)
	var bufPool *pool.BufferPool
	ownPool := false
	if cfg.bufferPool != nil {
		bufPool = cfg.bufferPool
	} else {
		bufPool = pool.NewBufferPool(
			cfg.initialBufferSize,
			cfg.maxBufferSize,
			pool.WithBoundedMode(true),
			pool.WithBoundedCapacity(2),
		)
		ownPool = true
	}
	return &RealtimeFixer{
		tsStore:        &TimestampStore{FirstChunk: true},
		buffer:         bufPool.Get(),
		bufferPool:     bufPool,
		ownBufferPool:  ownPool,
		maxBufferSize:  cfg.maxBufferSize,
		maxTagDataSize: cfg.maxTagDataSize,
		sourceHeaderOK: false,
		dedupCache:     NewDedupCache(MaxDedupCacheSize, DedupWindowMs),
		dupCount:       0,
	}
}

func (rf *RealtimeFixer) SetTimestampJumpReporter(reporter TimestampJumpReporter) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.jumpReporter = reporter
}

func (rf *RealtimeFixer) GetDedupStats() (duplicates int64, cacheSize int, cacheCapacity int) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	size, capacity := rf.dedupCache.GetStatsUnsafe()
	return rf.dupCount, size, capacity
}

// prepareOutput resets the persistent output buffer for the next Fix call.
func (rf *RealtimeFixer) prepareOutput() {
	rf.outputBuf.Reset()
}

// shrinkOutputBuffer releases an oversized output buffer after a large chunk is processed.
func (rf *RealtimeFixer) shrinkOutputBuffer() {
	if rf.outputBuf.Cap() > rf.maxBufferSize {
		rf.outputBuf = bytes.Buffer{}
	}
}

// Fix processes incoming bytes and returns fixed FLV data.
// Return semantics:
//   - (nil, nil) means "no complete output tag is ready yet" (normal for streaming).
//   - non-nil []byte contains serialized FLV tags ready for downstream processing.
func (rf *RealtimeFixer) Fix(input []byte) ([]byte, error) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	rf.buffer.Write(input)
	parsedTags := 0

	rf.prepareOutput()
	output := &rf.outputBuf

	// Consume the FLV file header once.
	// Rotation segments start at a tag boundary (no file header), so skip straight through.
	// Segment 0 must begin with FLV magic bytes or the stream is invalid.
	needsHeaderCheck := !rf.sourceHeaderOK && rf.buffer.Len() >= FlvHeaderSize
	if needsHeaderCheck {
		isFlvMagic := bytes.Equal(rf.buffer.Bytes()[:3], []byte{'F', 'L', 'V'})
		switch {
		case isFlvMagic:
			rf.buffer.Next(FlvHeaderSize) // consume 9-byte header; PrevTagSize0 stays for tag loop
		case rf.isRotationSegment:
			// carried bytes start at a tag boundary — nothing to consume
		default:
			return nil, ErrNotFlvFile
		}
		rf.sourceHeaderOK = true
	}

	// Parse complete tags from buffer
	for {
		availableBytes := rf.buffer.Bytes()

		// Need PreviousTagSize (4) + TagHeader (11) minimum
		if len(availableBytes) < PrevTagSizeBytes+TagHeaderSize {
			break
		}

		headerSlice := availableBytes[PrevTagSizeBytes : PrevTagSizeBytes+TagHeaderSize]

		tagType := headerSlice[0]
		dataSize := uint32(headerSlice[1])<<16 | uint32(headerSlice[2])<<8 | uint32(headerSlice[3])

		totalRequired := PrevTagSizeBytes + TagHeaderSize + int(dataSize)
		if len(availableBytes) < totalRequired {
			break
		}

		if err := validateCompleteTagHeader(tagType, dataSize, rf.maxTagDataSize); err != nil {
			return nil, err
		}

		payloadStart := PrevTagSizeBytes + TagHeaderSize
		payloadEnd := payloadStart + int(dataSize)
		// WARNING: tagData aliases rf.buffer's underlying bytes; keep usage synchronous.
		tagData := availableBytes[payloadStart:payloadEnd]
		// Consume only after full-tag length check succeeds.
		rf.buffer.Next(totalRequired)
		parsedTags++

		// Parse timestamp (24bit + 8bit extended)
		timestamp := int32(headerSlice[7])<<24 | int32(headerSlice[4])<<16 |
			int32(headerSlice[5])<<8 | int32(headerSlice[6])

		// Create tag
		tag := tagPool.Get().(*Tag)
		tag.Reset()
		tag.Type = tagType
		tag.DataSize = dataSize
		tag.Timestamp = timestamp
		tag.Data = tagData
		copy(tag.StreamID[:], headerSlice[8:11])

		// Detect header/keyframe
		if len(tagData) >= 2 {
			switch tagType {
			case TagTypeVideo:
				firstByte := tagData[0]
				secondByte := tagData[1]
				tag.IsKeyframe = (firstByte & 0xF0) == 0x10
				tag.IsHeader = secondByte == 0x00
			case TagTypeAudio:
				if (tagData[0]>>4) == 10 && len(tagData) >= 2 { // AAC
					tag.IsHeader = tagData[1] == 0x00
				}
			}
		}

		if rf.dedupCache.IsDuplicateUnsafe(tag) {
			rf.dupCount++
			tag.Reset()
			tagPool.Put(tag)
			if int(dataSize) >= rf.maxBufferSize {
				rf.compactBufferIfNeeded()
			}
			continue
		}

		rf.fixTimestamp(tag)

		if err := writeTagOptimized(output, tag); err != nil {
			rf.outputBuf.Reset()
			return nil, err
		}

		tag.Reset()
		tagPool.Put(tag)
		if int(dataSize) >= rf.maxBufferSize {
			rf.compactBufferIfNeeded()
		}
	}

	if parsedTags == 0 && rf.buffer.Len() > rf.maxBufferSize {
		rf.trimParseBufferToTail(rf.maxBufferSize)
	}

	if rf.tsStore.LastOriginal > 0 {
		shouldCleanByTime := rf.tsStore.LastOriginal-rf.lastDedupClean > 500
		shouldCleanByTagCount := parsedTags >= 128
		shouldCleanByHighWater := rf.dedupCache.IsAboveHighWaterUnsafe()
		if shouldCleanByTime || shouldCleanByTagCount || shouldCleanByHighWater {
			if shouldCleanByHighWater {
				rf.dedupCache.CleanHighWaterUnsafe(rf.tsStore.LastOriginal)
			} else {
				rf.dedupCache.CleanOldUnsafe(rf.tsStore.LastOriginal)
			}
			rf.lastDedupClean = rf.tsStore.LastOriginal
		}
	}

	rf.fixCalls++
	if rf.fixCalls%16 == 0 {
		rf.compactBufferIfNeeded()
	}
	if parsedTags > 0 {
		c, l := rf.buffer.Cap(), rf.buffer.Len()
		if c > rf.maxBufferSize && l*4 < c {
			rf.compactBufferIfNeeded()
		}
	}

	if output.Len() == 0 {
		rf.shrinkOutputBuffer()
		return nil, nil
	}

	result := make([]byte, output.Len())
	copy(result, output.Bytes())
	rf.shrinkOutputBuffer()

	return result, nil
}

// ResetTimestampStore resets the timestamp offset for a new segment.
// This should be called when rotating to a new segment file so that
// the new segment's timestamps start from 0 instead of continuing
// from the previous segment's time range.
func (rf *RealtimeFixer) ResetTimestampStore() {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.tsStore != nil {
		rf.tsStore.Reset()
	}
	rf.isRotationSegment = true
	rf.sourceHeaderOK = false
}

// ResetDedupCache clears the deduplication cache, which can be useful when starting a new segment to avoid false positives from old tags.
func (rf *RealtimeFixer) ResetDedupCache() {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.dedupCache != nil {
		rf.dedupCache.Reset()
	}
}

func (rf *RealtimeFixer) Close() {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.buffer != nil {
		buf := rf.buffer
		rf.buffer = nil
		if rf.bufferPool != nil && buf.Cap() <= rf.maxBufferSize {
			buf.Reset()
			rf.bufferPool.Put(buf)
		}
	}

	rf.outputBuf = bytes.Buffer{}

	if rf.dedupCache != nil {
		rf.dedupCache.Reset()
	}

	if rf.tsStore != nil {
		rf.tsStore.Reset()
	}

	if rf.ownBufferPool {
		rf.bufferPool = nil
	}
	rf.sourceHeaderOK = false
}

// compactBufferIfNeeded shrinks rf.buffer when capacity is much larger than used length.
func (rf *RealtimeFixer) compactBufferIfNeeded() {
	if rf.buffer == nil || rf.bufferPool == nil {
		return
	}
	c := rf.buffer.Cap()
	l := rf.buffer.Len()
	max := rf.maxBufferSize

	needsCompact := (c > max && l <= c/2) || c > 4*max
	if !needsCompact {
		return
	}

	newBuf := rf.bufferPool.Get()
	newBuf.Reset()
	if l > 0 {
		newBuf.Write(rf.buffer.Bytes())
	}
	old := rf.buffer
	rf.buffer = newBuf
	rf.bufferPool.Put(old)
}

// trimParseBufferToTail keeps only the trailing window that may belong to a
// partial FLV tag when no complete tag could be parsed and the buffer exceeds
// the configured limit. Prevents unbounded growth on non-tag padding bytes.
func (rf *RealtimeFixer) trimParseBufferToTail(keep int) {
	if rf.buffer == nil || keep <= 0 {
		return
	}
	b := rf.buffer.Bytes()
	if len(b) <= keep {
		return
	}
	rf.buffer.Reset()
	rf.buffer.Write(b[len(b)-keep:])
}

func (rf *RealtimeFixer) fixTimestamp(tag *Tag) {
	ts := rf.tsStore
	currentTimestamp := tag.Timestamp
	previousTimestamp := ts.LastOriginal
	previousOffset := ts.CurrentOffset
	wasFirstChunk := ts.FirstChunk
	var jumpWarning *TimestampJumpWarning

	if ts.FirstChunk {
		ts.FirstChunk = false
		ts.CurrentOffset = currentTimestamp
	}

	diff := currentTimestamp - previousTimestamp

	if diff < -JumpThreshold || (ts.LastOriginal == 0 && diff < 0) {
		jumpWarning = &TimestampJumpWarning{
			CurrentTimestamp:  currentTimestamp,
			PreviousTimestamp: previousTimestamp,
			Delta:             diff,
			PreviousOffset:    previousOffset,
			IsRotationSegment: rf.isRotationSegment,
			TagType:           tag.Type,
		}
		ts.CurrentOffset = currentTimestamp - ts.NextTimestampTarget
	} else if diff > JumpThreshold {
		jumpWarning = &TimestampJumpWarning{
			CurrentTimestamp:  currentTimestamp,
			PreviousTimestamp: previousTimestamp,
			Delta:             diff,
			PreviousOffset:    previousOffset,
			IsRotationSegment: rf.isRotationSegment,
			TagType:           tag.Type,
		}
		ts.CurrentOffset = currentTimestamp - ts.NextTimestampTarget
	}

	ts.LastOriginal = currentTimestamp
	if jumpWarning != nil {
		jumpWarning.AppliedOffset = ts.CurrentOffset
		skipTransientResetJump := rf.isRotationSegment && jumpWarning.PreviousTimestamp == 0 && ts.NextTimestampTarget > 0
		if rf.jumpReporter != nil && !wasFirstChunk && !skipTransientResetJump {
			rf.jumpReporter(*jumpWarning)
		}
	}

	tag.Timestamp -= ts.CurrentOffset
	if tag.Timestamp < 0 {
		tag.Timestamp = 0
	}

	ts.NextTimestampTarget = CalculateNextTarget(tag)
}
