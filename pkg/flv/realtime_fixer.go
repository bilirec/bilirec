package flv

import (
	"bytes"
	"sync"
)

// =====================================================
// REALTIME FIXER - 逐個 Tag 修復並輸出
// =====================================================

type RealtimeFixer struct {
	mu                sync.Mutex
	tsStore           *TimestampStore
	buffer            *bytes.Buffer
	sourceHeaderOK    bool
	isRotationSegment bool        // set by ResetTimestampStore; rotation segments have no FLV file header
	dedupCache        *DedupCache // 🔥 新增:  去重緩存
	dupCount          int64       // 🔥 新增: 重複計數
	lastDedupClean    int32       // timestamp of last dedup clean
	jumpReporter      TimestampJumpReporter
}

func NewRealtimeFixer() *RealtimeFixer {
	return &RealtimeFixer{
		tsStore:        &TimestampStore{FirstChunk: true},
		buffer:         realtimeBufferPool.Get(), // 🔥 優化: 從 pool 取得
		sourceHeaderOK: false,
		dedupCache:     NewDedupCache(MaxDedupCacheSize, DedupWindowMs), // 🔥 初始化去重
		dupCount:       0,
	}
}

func (rf *RealtimeFixer) SetTimestampJumpReporter(reporter TimestampJumpReporter) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.jumpReporter = reporter
}

// 🔥 新增: 獲取去重統計
func (rf *RealtimeFixer) GetDedupStats() (duplicates int64, cacheSize int, cacheCapacity int) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	size, capacity := rf.dedupCache.GetStats()
	return rf.dupCount, size, capacity
}

// Fix processes incoming bytes and returns fixed FLV data
func (rf *RealtimeFixer) Fix(input []byte) ([]byte, error) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	rf.buffer.Write(input)

	// 🔥 優化: 從 pool 取得輸出 buffer
	output := realtimeBufferPool.Get()
	output.Reset()

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
			output.Reset()
			realtimeBufferPool.Put(output)
			return nil, ErrNotFlvFile
		}
		rf.sourceHeaderOK = true
	}

	headerBytes := headerBytesPool.GetBytes()
	defer headerBytesPool.PutBytes(headerBytes)

	// Parse complete tags from buffer
	for {
		// Need PreviousTagSize (4) + TagHeader (11) minimum
		if rf.buffer.Len() < 15 {
			break
		}

		// Skip PreviousTagSize
		rf.buffer.Next(PrevTagSizeBytes)

		// Peek tag header
		// Not enough bytes for header yet: rebuild PrevTagSize + remaining bytes safely.
		if rf.buffer.Len() < TagHeaderSize {
			tmp := realtimeBufferPool.Get()
			tmp.Reset()
			tmp.Write([]byte{0, 0, 0, 0}) // PrevTagSize
			tmp.Write(rf.buffer.Bytes())
			rf.buffer.Reset()
			rf.buffer.Write(tmp.Bytes())
			tmp.Reset()
			realtimeBufferPool.Put(tmp)
			break
		}

		// 🔥 優化: 從 pool 取得 header buffer
		rf.buffer.Read(headerBytes)

		tagType := headerBytes[0]
		dataSize := uint32(headerBytes[1])<<16 | uint32(headerBytes[2])<<8 | uint32(headerBytes[3])

		// Check if we have complete tag data
		if rf.buffer.Len() < int(dataSize) {
			// Need more bytes: reconstruct PrevTagSize + header + current remainder
			tempBuf := realtimeBufferPool.Get()
			tempBuf.Reset()
			tempBuf.Write([]byte{0, 0, 0, 0}) // PrevTagSize
			tempBuf.Write(headerBytes)        // use headerBytes while valid
			tempBuf.Write(rf.buffer.Bytes())

			rf.buffer.Reset()
			rf.buffer.Write(tempBuf.Bytes())

			tempBuf.Reset()
			realtimeBufferPool.Put(tempBuf)
			break
		}

		// 🔥 優化: 從 pool 取得指定長度的 tagData 切片
		tagData := tagDataSlicePool.GetSized(int(dataSize))
		rf.buffer.Read(tagData)

		// Parse timestamp (24bit + 8bit extended)
		timestamp := int32(headerBytes[7])<<24 | int32(headerBytes[4])<<16 |
			int32(headerBytes[5])<<8 | int32(headerBytes[6])

		// Create tag
		tag := tagPool.Get().(*Tag)
		tag.Reset()
		tag.Type = tagType
		tag.DataSize = dataSize
		tag.Timestamp = timestamp
		tag.Data = tagData
		copy(tag.StreamID[:], headerBytes[8:11])

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

		// 🔥 新增: 去重檢查 (在修復時間戳之前)
		if rf.dedupCache.IsDuplicate(tag) {
			rf.dupCount++
			// 回收 tagData 到 pool
			tagDataSlicePool.Put(tagData)
			tag.Reset() // clear Data and other fields before pooling
			tagPool.Put(tag)
			continue // 跳過重複的 tag
		}

		// Fix timestamp
		rf.fixTimestamp(tag)

		// Write fixed tag
		if err := writeTagOptimized(output, tag); err != nil {
			// 回收 tagData 到 pool
			tagDataSlicePool.Put(tagData)
			output.Reset()
			realtimeBufferPool.Put(output)
			return nil, err
		}

		// 🔥 優化:  返還 tagData 到 pool (在清空 tag 引用之前)
		tagDataSlicePool.Put(tagData)
		tag.Reset()
		tagPool.Put(tag)
	}

	// 🔥 新增: 定期清理過期去重記錄
	// 🔥 FIX: Clean more frequently (every 500ms instead of 1000ms) to prevent memory buildup
	if rf.tsStore.LastOriginal > 0 {
		if rf.tsStore.LastOriginal-rf.lastDedupClean > 500 {
			rf.dedupCache.CleanOld(rf.tsStore.LastOriginal)
			rf.lastDedupClean = rf.tsStore.LastOriginal
		}
	}

	// Try to compact the internal buffer if it grew large
	rf.compactBufferIfNeeded()

	// 🔥 優化:  返回複製的數據，這樣 output buffer 可以被復用
	result := make([]byte, output.Len())
	copy(result, output.Bytes())

	output.Reset()
	realtimeBufferPool.Put(output)

	return result, nil
}

// 🔥 優化:  釋放資源
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
	// Mark as rotation segment so Fix() skips the FLV file header check.
	// Rotation segments start with split-detector-injected carried bytes, not a FLV header.
	rf.isRotationSegment = true
	rf.sourceHeaderOK = false
}

// ResetDedupCache clears the deduplication cache, which can be useful when starting a new segment to avoid false positives from old tags.
// currently not used because the dedup cache automatically expires old entries based on timestamp, but can be called manually if needed (e.g. on segment rotation).
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
		rf.buffer.Reset()
		realtimeBufferPool.Put(rf.buffer)
		rf.buffer = nil
	}

	if rf.dedupCache != nil {
		rf.dedupCache.Reset()
	}

	// Reset other session state
	if rf.tsStore != nil {
		rf.tsStore.Reset()
	}
	rf.sourceHeaderOK = false
}

// compactBufferIfNeeded shrinks rf.buffer when capacity is much larger than used length.
func (rf *RealtimeFixer) compactBufferIfNeeded() {
	if rf.buffer == nil {
		return
	}
	c := rf.buffer.Cap()
	l := rf.buffer.Len()

	// Heuristic: if buffer is very large (>> MaxBufferSize) and largely empty, shrink it.
	if c > MaxBufferSize && l <= c/4 {
		newBuf := realtimeBufferPool.Get()
		newBuf.Reset()
		if l > 0 {
			newBuf.Write(rf.buffer.Bytes())
		}
		old := rf.buffer
		rf.buffer = newBuf
		// Return old to pool (Put only keeps buffers <= maxCap; otherwise allow GC)
		realtimeBufferPool.Put(old)
	}
}
func (rf *RealtimeFixer) fixTimestamp(tag *Tag) {
	ts := rf.tsStore
	currentTimestamp := tag.Timestamp
	previousTimestamp := ts.LastOriginal
	previousOffset := ts.CurrentOffset
	wasFirstChunk := ts.FirstChunk
	var jumpWarning *TimestampJumpWarning

	// First chunk special handling
	if ts.FirstChunk {
		ts.FirstChunk = false
		ts.CurrentOffset = currentTimestamp
	}

	diff := currentTimestamp - previousTimestamp

	// Detect timestamp jump
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
		// Skip reporting for the very first tag only.
		// Also suppress reset-transition false positives where the previous timestamp is 0ms
		// (e.g., carried config/header tags followed by a large live timestamp).
		skipTransientResetJump := rf.isRotationSegment && jumpWarning.PreviousTimestamp == 0 && ts.NextTimestampTarget > 0
		if rf.jumpReporter != nil && !wasFirstChunk && !skipTransientResetJump {
			rf.jumpReporter(*jumpWarning)
		}
	}

	// Apply offset
	tag.Timestamp -= ts.CurrentOffset
	if tag.Timestamp < 0 {
		// FLV timestamp is unsigned in file format; negative values would be
		// serialized as huge wraparound numbers (e.g. 4294967s start time).
		tag.Timestamp = 0
	}

	// Calculate next target
	ts.NextTimestampTarget = CalculateNextTarget(tag)
}
