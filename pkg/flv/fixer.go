package flv

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"sync"

	"github.com/bilirec/bilirec/pkg/pool"
)

const (
	TagTypeAudio  = 0x08
	TagTypeVideo  = 0x09
	TagTypeScript = 0x12

	JumpThreshold = 500

	AudioDurationFallback = 22
	AudioDurationMin      = 20
	AudioDurationMax      = 24

	VideoDurationFallback = 33
	VideoDurationMin      = 15
	VideoDurationMax      = 50

	// 🔥 優化: Buffer 大小常量
	DefaultBufferSize = 8 * 1024  // 8KB - Raspberry Pi 友好
	MaxBufferSize     = 64 * 1024 // 64KB - 最大緩衝
	TagHeaderSize     = 11
	FlvHeaderSize     = 9
	PrevTagSizeBytes  = 4

	// 🔥 新增: 去重相關常量
	MaxDedupCacheSize = 1000 // 最大去重緩存大小
	DedupWindowMs     = 5000 // 去重時間窗口 (毫秒)

	// dedupCacheHighWaterNum/Den: trigger proactive cleanup at 80% capacity.
	dedupCacheHighWaterNum = 4
	dedupCacheHighWaterDen = 5
	// dedupCacheHighWaterEvictDenom: after time-based clean, FIFO-evict 1/N oldest if still high.
	dedupCacheHighWaterEvictDenom = 10
)

var (
	FlvHeader = []byte{'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09}

	ErrNotFlvFile      = errors.New("不是有效的 FLV 文件")
	ErrInvalidTag      = errors.New("无效的 FLV 标签")
	ErrBufferCorrupted = errors.New("检测到缓冲区损坏")

	// Dedicated pool for AccumulateFixer batch processing.
	accumulateBufferPool = pool.NewBufferPool(DefaultBufferSize, MaxBufferSize)
	// Dedicated pool for RealtimeFixer to isolate frequent live-room start/stop churn.
	realtimeBufferPool = pool.NewBufferPool(DefaultBufferSize, MaxBufferSize)
	// Dedicated pool for HeaderChangeDetector to isolate parser scratch usage.
	headerDetectorBufferPool = pool.NewBufferPool(DefaultBufferSize, MaxBufferSize)

	tagPool = sync.Pool{
		New: func() any {
			return &Tag{}
		},
	}

	headerBytesPool = pool.NewBytesPool(TagHeaderSize)
	smallBytesPool  = pool.NewBytesPool(PrevTagSizeBytes)
)

// Tag represents a complete FLV tag
type Tag struct {
	Type      byte
	DataSize  uint32
	Timestamp int32
	StreamID  [3]byte
	// WARNING: in RealtimeFixer hot-path this may alias internal parser buffer memory.
	// Do not retain this slice beyond the current synchronous processing step.
	Data       []byte
	IsHeader   bool
	IsKeyframe bool
}

// 🔥 優化:  重置 Tag 以便復用
func (t *Tag) Reset() {
	t.Type = 0
	t.DataSize = 0
	t.Timestamp = 0
	t.StreamID = [3]byte{0, 0, 0}
	t.Data = nil
	t.IsHeader = false
	t.IsKeyframe = false
}

// TimestampStore tracks timestamp fixing state (session-based)
type TimestampStore struct {
	FirstChunk          bool
	LastOriginal        int32
	CurrentOffset       int32
	NextTimestampTarget int32
}

type TimestampJumpWarning struct {
	CurrentTimestamp  int32
	PreviousTimestamp int32
	Delta             int32
	AppliedOffset     int32
	PreviousOffset    int32
	IsRotationSegment bool
	TagType           byte
}

type TimestampJumpReporter func(TimestampJumpWarning)

func (ts *TimestampStore) Reset() {
	ts.FirstChunk = true
	ts.LastOriginal = 0
	ts.CurrentOffset = 0
	ts.NextTimestampTarget = 0
}

// 🔥 新增: 去重記錄結構
type TagSignature struct {
	Hash      uint64
	Timestamp int32
	Type      byte
	DataSize  uint32
}

// 🔥 新增: 去重緩存管理器
type DedupCache struct {
	mu         sync.Mutex
	signatures map[uint64]*TagSignature // hash -> signature
	order      []uint64                 // 用於 FIFO 清理
	maxSize    int
	windowMs   int32
}

func NewDedupCache(maxSize int, windowMs int32) *DedupCache {
	return &DedupCache{
		signatures: make(map[uint64]*TagSignature, maxSize),
		order:      make([]uint64, 0, maxSize),
		maxSize:    maxSize,
		windowMs:   windowMs,
	}
}

// 計算 Tag 的唯一簽名
func (dc *DedupCache) computeSignature(tag *Tag) uint64 {
	// Inline FNV-1a to avoid hash interface dispatch on the hot path.
	const (
		fnvOffset64 = 1469598103934665603
		fnvPrime64  = 1099511628211
	)

	hash := uint64(fnvOffset64)

	hash ^= uint64(tag.Type)
	hash *= fnvPrime64

	hash ^= uint64(uint32(tag.Timestamp))
	hash *= fnvPrime64

	hash ^= uint64(tag.DataSize)
	hash *= fnvPrime64

	// 只用前32字節數據計算 hash (平衡性能和準確性)
	data := tag.Data
	if len(data) > 32 {
		data = data[:32]
	}
	for _, b := range data {
		hash ^= uint64(b)
		hash *= fnvPrime64
	}

	return hash
}

// 檢查是否為重複 Tag
func (dc *DedupCache) IsDuplicate(tag *Tag) bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	return dc.isDuplicateLocked(tag)
}

// IsDuplicateUnsafe skips locking. Caller must guarantee synchronization
// (e.g. by holding RealtimeFixer.mu for the entire access).
func (dc *DedupCache) IsDuplicateUnsafe(tag *Tag) bool {
	return dc.isDuplicateLocked(tag)
}

func (dc *DedupCache) isDuplicateLocked(tag *Tag) bool {
	hash := dc.computeSignature(tag)

	// 檢查是否存在相同簽名
	if existing, found := dc.signatures[hash]; found {
		// 檢查時間窗口
		timeDiff := tag.Timestamp - existing.Timestamp
		if timeDiff < 0 {
			timeDiff = -timeDiff
		}

		// 如果在時間窗口內且類型、大小都匹配，判定為重複
		if timeDiff <= dc.windowMs &&
			existing.Type == tag.Type &&
			existing.DataSize == tag.DataSize {
			return true
		}
	}

	// 添加到緩存
	dc.add(hash, &TagSignature{
		Hash:      hash,
		Timestamp: tag.Timestamp,
		Type:      tag.Type,
		DataSize:  tag.DataSize,
	})

	return false
}

// 添加簽名到緩存 (內部方法，已加鎖)
func (dc *DedupCache) add(hash uint64, sig *TagSignature) {
	// 如果已存在，更新時間戳
	if _, found := dc.signatures[hash]; found {
		dc.signatures[hash] = sig
		return
	}

	// 檢查緩存大小，執行 FIFO 清理
	if len(dc.signatures) >= dc.maxSize {
		// 🔥 FIX: Remove 20% instead of 10% to reduce frequency of cleanups
		removeCount := dc.maxSize / 5
		if removeCount < 1 {
			removeCount = 1
		}
		dc.evictOldestLocked(removeCount)
	}

	// 添加新記錄
	dc.signatures[hash] = sig
	dc.order = append(dc.order, hash)
}

// 清理過期記錄 (基於時間窗口)
func (dc *DedupCache) CleanOld(currentTimestamp int32) {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	dc.cleanOldLocked(currentTimestamp)
}

// CleanOldUnsafe skips locking. Caller must guarantee synchronization
// (e.g. by holding RealtimeFixer.mu for the entire access).
func (dc *DedupCache) CleanOldUnsafe(currentTimestamp int32) {
	dc.cleanOldLocked(currentTimestamp)
}

// CleanHighWaterUnsafe runs time-based cleanup and, if still above the high-water
// threshold, FIFO-evicts a small oldest batch. No allocations on the hot path.
func (dc *DedupCache) CleanHighWaterUnsafe(currentTimestamp int32) {
	dc.cleanHighWaterLocked(currentTimestamp)
}

// IsAboveHighWaterUnsafe reports whether the cache is at or above 80% capacity.
func (dc *DedupCache) IsAboveHighWaterUnsafe() bool {
	return len(dc.signatures) >= dc.highWaterSize()
}

func (dc *DedupCache) highWaterSize() int {
	return dc.maxSize * dedupCacheHighWaterNum / dedupCacheHighWaterDen
}

func (dc *DedupCache) cleanHighWaterLocked(currentTimestamp int32) {
	if len(dc.signatures) < dc.highWaterSize() {
		return
	}
	dc.cleanOldLocked(currentTimestamp)
	if len(dc.signatures) < dc.highWaterSize() {
		return
	}
	removeCount := dc.maxSize / dedupCacheHighWaterEvictDenom
	if removeCount < 1 {
		removeCount = 1
	}
	dc.evictOldestLocked(removeCount)
}

func (dc *DedupCache) evictOldestLocked(removeCount int) {
	if removeCount < 1 || len(dc.order) == 0 {
		return
	}
	if removeCount > len(dc.order) {
		removeCount = len(dc.order)
	}
	for _, oldHash := range dc.order[:removeCount] {
		delete(dc.signatures, oldHash)
	}
	copy(dc.order, dc.order[removeCount:])
	dc.order = dc.order[:len(dc.order)-removeCount]
}

func (dc *DedupCache) cleanOldLocked(currentTimestamp int32) {
	// 🔥 FIX: Reuse the existing slice to avoid allocations
	writeIdx := 0

	for _, hash := range dc.order {
		sig := dc.signatures[hash]
		timeDiff := currentTimestamp - sig.Timestamp
		if timeDiff < 0 {
			timeDiff = -timeDiff
		}

		// 保留在窗口內的記錄
		if timeDiff <= dc.windowMs*2 { // 保留2倍窗口以容錯
			dc.order[writeIdx] = hash
			writeIdx++
		} else {
			delete(dc.signatures, hash)
		}
	}

	// Trim the slice without reallocating
	dc.order = dc.order[:writeIdx]
}

// 重置緩存
func (dc *DedupCache) Reset() {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	dc.signatures = make(map[uint64]*TagSignature, dc.maxSize)
	dc.order = make([]uint64, 0, dc.maxSize)
}

// 獲取統計信息
func (dc *DedupCache) GetStats() (size int, capacity int) {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	return dc.getStatsLocked()
}

// GetStatsUnsafe skips locking. Caller must guarantee synchronization
// (e.g. by holding RealtimeFixer.mu for the entire access).
func (dc *DedupCache) GetStatsUnsafe() (size int, capacity int) {
	return dc.getStatsLocked()
}

func (dc *DedupCache) getStatsLocked() (size int, capacity int) {
	return len(dc.signatures), dc.maxSize
}

// =====================================================
// Helper:  Write Tag to Stream
// =====================================================

func writeTagOptimized(w io.Writer, tag *Tag) error {
	// 🔥 優化: 從 pool 取得 header buffer
	header := headerBytesPool.GetBytes()
	defer headerBytesPool.PutBytes(header)

	header[0] = tag.Type

	header[1] = byte(tag.DataSize >> 16)
	header[2] = byte(tag.DataSize >> 8)
	header[3] = byte(tag.DataSize)

	header[4] = byte(tag.Timestamp >> 16)
	header[5] = byte(tag.Timestamp >> 8)
	header[6] = byte(tag.Timestamp)
	header[7] = byte(tag.Timestamp >> 24)

	copy(header[8:11], tag.StreamID[:])

	if _, err := w.Write(header); err != nil {
		return err
	}

	if _, err := w.Write(tag.Data); err != nil {
		return err
	}

	// 🔥 優化:  從 pool 取得 prevTagSize buffer
	prevTagSize := smallBytesPool.GetBytes()
	defer smallBytesPool.PutBytes(prevTagSize)

	binary.BigEndian.PutUint32(prevTagSize, uint32(11+len(tag.Data)))
	if _, err := w.Write(prevTagSize); err != nil {
		return err
	}

	return nil
}

func WriteTag(w io.Writer, tag *Tag) error {
	return writeTagOptimized(w, tag)
}

// =====================================================
// Advanced: Group-based Timestamp Calculation
// =====================================================

func CalculateNextTargetAdvanced(tags []*Tag) int32 {
	videoTags := make([]*Tag, 0)
	audioTags := make([]*Tag, 0)

	for _, tag := range tags {
		switch tag.Type {
		case TagTypeVideo:
			videoTags = append(videoTags, tag)
		case TagTypeAudio:
			audioTags = append(audioTags, tag)
		}
	}

	videoDuration := int32(0)
	if len(videoTags) >= 2 {
		duration := videoTags[1].Timestamp - videoTags[0].Timestamp
		if duration >= VideoDurationMin && duration <= VideoDurationMax {
			videoDuration = videoTags[len(videoTags)-1].Timestamp + duration
		} else {
			videoDuration = videoTags[len(videoTags)-1].Timestamp + VideoDurationFallback
		}
	} else if len(videoTags) == 1 {
		videoDuration = videoTags[0].Timestamp + VideoDurationFallback
	}

	audioDuration := int32(0)
	if len(audioTags) >= 2 {
		duration := audioTags[1].Timestamp - audioTags[0].Timestamp
		if duration >= AudioDurationMin && duration <= AudioDurationMax {
			audioDuration = audioTags[len(audioTags)-1].Timestamp + duration
		} else {
			audioDuration = audioTags[len(audioTags)-1].Timestamp + AudioDurationFallback
		}
	} else if len(audioTags) == 1 {
		audioDuration = audioTags[0].Timestamp + AudioDurationFallback
	}

	return int32(math.Max(float64(videoDuration), float64(audioDuration)))
}

func CalculateNextTarget(tag *Tag) int32 {
	duration := int32(VideoDurationFallback)
	if tag.Type == TagTypeAudio {
		duration = AudioDurationFallback
	}
	return tag.Timestamp + duration
}
