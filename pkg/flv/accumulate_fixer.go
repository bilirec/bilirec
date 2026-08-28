package flv

import (
	"bytes"
	"sync"
)

// =====================================================
// ACCUMULATE FIXER - 累積 X MB 後批次處理
// =====================================================

type AccumulateFixer struct {
	mu             sync.Mutex
	tsStore        *TimestampStore
	buffer         *bytes.Buffer
	chunkSizeBytes int
	headerWritten  bool
	totalProcessed int64

	// 🔥 優化: 預分配 tag slice
	tagCache     []*Tag
	tagCacheSize int

	// 🔥 新增: 去重支持
	dedupCache   *DedupCache
	dupCount     int64
	jumpReporter TimestampJumpReporter
}

func NewAccumulateFixer(chunkSizeMB int) *AccumulateFixer {

	// 🔥 優化:  估算可能的 tag 數量並預分配
	estimatedTags := (chunkSizeMB * 1024 * 1024) / 1024 // 假設平均 1KB/tag

	return &AccumulateFixer{
		tsStore:        &TimestampStore{FirstChunk: true},
		buffer:         new(bytes.Buffer),
		chunkSizeBytes: chunkSizeMB * 1024 * 1024,
		headerWritten:  false,
		tagCache:       make([]*Tag, 0, estimatedTags),
		tagCacheSize:   estimatedTags,
		dedupCache:     NewDedupCache(MaxDedupCacheSize, DedupWindowMs), // 🔥 初始化去重
		dupCount:       0,
	}
}

func (af *AccumulateFixer) SetTimestampJumpReporter(reporter TimestampJumpReporter) {
	af.mu.Lock()
	defer af.mu.Unlock()
	af.jumpReporter = reporter
}

// 🔥 新增: 獲取去重統計
func (af *AccumulateFixer) GetDedupStats() (duplicates int64, cacheSize int, cacheCapacity int) {
	af.mu.Lock()
	defer af.mu.Unlock()

	size, capacity := af.dedupCache.GetStats()
	return af.dupCount, size, capacity
}

// Accumulate adds data and returns true if ready to flush
func (af *AccumulateFixer) Accumulate(input []byte) (bool, error) {
	af.mu.Lock()
	defer af.mu.Unlock()

	af.buffer.Write(input)
	return af.buffer.Len() >= af.chunkSizeBytes, nil
}

// Flush processes accumulated data (call this when Accumulate returns true OR at EOF)
func (af *AccumulateFixer) Flush() ([]byte, error) {
	af.mu.Lock()
	defer af.mu.Unlock()

	return af.flushInternal()
}

// FlushRemaining processes all remaining data (call at EOF)
func (af *AccumulateFixer) FlushRemaining() ([]byte, error) {
	af.mu.Lock()
	defer af.mu.Unlock()

	// Force flush even if buffer is small
	return af.flushInternal()
}

// 🔥 優化:  釋放資源
func (af *AccumulateFixer) Close() {
	af.mu.Lock()
	defer af.mu.Unlock()

	if af.buffer != nil {
		af.buffer.Reset()
		accumulateBufferPool.Put(af.buffer)
		af.buffer = nil
	}

	// 返還所有 tag 到 pool
	for _, tag := range af.tagCache {
		if tag != nil {
			tagPool.Put(tag)
		}
	}
	af.tagCache = nil

	if af.dedupCache != nil {
		af.dedupCache.Reset()
	}
}

func (af *AccumulateFixer) flushInternal() ([]byte, error) {
	if af.buffer.Len() == 0 {
		return nil, nil
	}

	// 🔥 優化: 從 pool 取得 output buffer
	output := accumulateBufferPool.Get()
	output.Reset()

	// Write header once globally (not per flush)
	if !af.headerWritten {
		if af.buffer.Len() < 9 {
			// Not enough data yet, keep waiting
			return nil, nil
		}

		header := make([]byte, 9)
		copy(header, af.buffer.Bytes()[:9])

		if !bytes.Equal(header[:3], []byte{'F', 'L', 'V'}) {
			return nil, ErrNotFlvFile
		}

		output.Write(header)
		output.Write([]byte{0, 0, 0, 0})
		af.headerWritten = true
		af.buffer.Next(9) // Consume header from buffer
	}

	// Parse all complete tags
	// 🔥 優化: 重用 tag cache
	tags := af.tagCache[:0] // 保留容量，清空長度

	for af.buffer.Len() >= 15 {
		startLen := af.buffer.Len()

		// Skip PreviousTagSize
		// 🔥 優化: 從 pool 取得小 buffer
		prevTagSizeBytes := smallBytesPool.GetBytes()
		af.buffer.Read(prevTagSizeBytes)

		if af.buffer.Len() < TagHeaderSize {
			// Restore
			tempBuf := accumulateBufferPool.Get()
			tempBuf.Reset()
			tempBuf.Write(prevTagSizeBytes)
			tempBuf.Write(af.buffer.Bytes())
			af.buffer.Reset()
			af.buffer.Write(tempBuf.Bytes())
			tempBuf.Reset()
			accumulateBufferPool.Put(tempBuf)
			smallBytesPool.PutBytes(prevTagSizeBytes)
			break
		}

		headerBytes := headerBytesPool.GetBytes()
		af.buffer.Read(headerBytes)

		dataSize := uint32(headerBytes[1])<<16 | uint32(headerBytes[2])<<8 | uint32(headerBytes[3])

		if af.buffer.Len() < int(dataSize) {
			// Incomplete tag, restore buffer
			tempBuf := accumulateBufferPool.Get()
			tempBuf.Reset()
			tempBuf.Write(prevTagSizeBytes)
			tempBuf.Write(headerBytes)
			tempBuf.Write(af.buffer.Bytes())
			af.buffer.Reset()
			af.buffer.Write(tempBuf.Bytes())
			tempBuf.Reset()
			accumulateBufferPool.Put(tempBuf)
			headerBytesPool.PutBytes(headerBytes)
			smallBytesPool.PutBytes(prevTagSizeBytes)
			break
		}

		tagData := make([]byte, dataSize)
		af.buffer.Read(tagData)

		timestamp := int32(headerBytes[7])<<24 | int32(headerBytes[4])<<16 |
			int32(headerBytes[5])<<8 | int32(headerBytes[6])

		// 🔥 優化:  從 pool 取得 tag
		tag := tagPool.Get().(*Tag)
		tag.Reset()
		tag.Type = headerBytes[0]
		tag.DataSize = dataSize
		tag.Timestamp = timestamp
		tag.Data = tagData
		copy(tag.StreamID[:], headerBytes[8:11])

		if tag.Type == TagTypeVideo {
			_, tag.IsHeader, tag.IsKeyframe = ClassifyVideoTag(tagData)
		} else if tag.Type == TagTypeAudio && len(tagData) >= 2 && (tagData[0]>>4) == 10 {
			tag.IsHeader = tagData[1] == 0x00
		}

		// 🔥 新增:  去重檢查
		if af.dedupCache.IsDuplicate(tag) {
			af.dupCount++
			tagPool.Put(tag)
			headerBytesPool.PutBytes(headerBytes)
			smallBytesPool.PutBytes(prevTagSizeBytes)
			continue // 跳過重複的 tag
		}

		tags = append(tags, tag)

		headerBytesPool.PutBytes(headerBytes)
		smallBytesPool.PutBytes(prevTagSizeBytes)

		// Safety check
		if af.buffer.Len() > startLen {
			return nil, ErrBufferCorrupted
		}
	}

	// Fix timestamps for all tags
	af.fixTimestamps(tags)

	// Write all fixed tags
	for _, tag := range tags {
		if err := writeTagOptimized(output, tag); err != nil {
			return nil, err
		}
	}

	af.totalProcessed += int64(output.Len())

	// 🔥 新增: 定期清理過期去重記錄
	if len(tags) > 0 {
		lastTimestamp := tags[len(tags)-1].Timestamp
		af.dedupCache.CleanOld(lastTimestamp)
	}

	// 🔥 優化: 保存 tag cache 供下次使用
	af.tagCache = tags

	// 🔥 優化: 返回複製的數據
	result := make([]byte, output.Len())
	copy(result, output.Bytes())

	output.Reset()
	accumulateBufferPool.Put(output)

	return result, nil
}

func (af *AccumulateFixer) fixTimestamps(tags []*Tag) {
	if len(tags) == 0 {
		return
	}

	ts := af.tsStore

	// First chunk:  find minimum timestamp
	if ts.FirstChunk {
		ts.FirstChunk = false
		minTs := tags[0].Timestamp
		for _, t := range tags {
			if t.Timestamp < minTs {
				minTs = t.Timestamp
			}
		}
		ts.CurrentOffset = minTs
	}

	for _, tag := range tags {
		currentTimestamp := tag.Timestamp
		previousTimestamp := ts.LastOriginal
		previousOffset := ts.CurrentOffset
		diff := currentTimestamp - previousTimestamp
		var jumpWarning *TimestampJumpWarning

		if diff < -JumpThreshold || (ts.LastOriginal == 0 && diff < 0) {
			jumpWarning = &TimestampJumpWarning{
				CurrentTimestamp:  currentTimestamp,
				PreviousTimestamp: previousTimestamp,
				Delta:             diff,
				PreviousOffset:    previousOffset,
				TagType:           tag.Type,
			}
			ts.CurrentOffset = currentTimestamp - ts.NextTimestampTarget
		} else if diff > JumpThreshold {
			jumpWarning = &TimestampJumpWarning{
				CurrentTimestamp:  currentTimestamp,
				PreviousTimestamp: previousTimestamp,
				Delta:             diff,
				PreviousOffset:    previousOffset,
				TagType:           tag.Type,
			}
			ts.CurrentOffset = currentTimestamp - ts.NextTimestampTarget
		}

		ts.LastOriginal = tag.Timestamp
		if jumpWarning != nil {
			jumpWarning.AppliedOffset = ts.CurrentOffset
			if af.jumpReporter != nil {
				af.jumpReporter(*jumpWarning)
			}
		}
		tag.Timestamp -= ts.CurrentOffset
	}

	ts.NextTimestampTarget = CalculateNextTargetAdvanced(tags)
}

// GetStats returns processing statistics
func (af *AccumulateFixer) GetStats() (buffered int, processed int64) {
	af.mu.Lock()
	defer af.mu.Unlock()
	return af.buffer.Len(), af.totalProcessed
}
