package pool

import (
	"sort"
	"sync/atomic"
)

const (
	minBucketSize      = 64 * 1024
	maxBucketSize      = 4 * 1024 * 1024
	bucketAlignBytes   = 4 * 1024
	defaultBucketBase  = 512 * 1024
	defaultBucketCount = 5
)

type BucketedBytesPool struct {
	bucketSizes []int
	slots       []*boundablePool[[]byte]

	hits      atomic.Uint64
	misses    atomic.Uint64
	oversized atomic.Uint64
}

type BucketedBytesPoolStats struct {
	Hits      uint64
	Misses    uint64
	Oversized uint64
}

func NewBucketedBytesPool(baseSize int, opts ...BucketedBytesPoolOption) *BucketedBytesPool {
	cfg := applyPoolOptions(opts, PoolBoundedConfig{
		Mode:     BufferPoolModeSoft,
		Capacity: 4,
	})
	buckets := computeBucketSizes(baseSize)
	p := &BucketedBytesPool{
		bucketSizes: buckets,
		slots:       make([]*boundablePool[[]byte], len(buckets)),
	}
	for i, bucketSize := range buckets {
		size := bucketSize
		p.slots[i] = newBoundablePool(cfg,
			func() []byte { return make([]byte, size) },
			func(b []byte) []byte { return b },
			func(b []byte) ([]byte, bool) {
				if cap(b) != size {
					return b, false
				}
				return b[:size], true
			},
		)
	}
	return p
}

func (p *BucketedBytesPool) GetSized(size int) []byte {
	if size <= 0 {
		return []byte{}
	}
	idx := p.findBucketIndex(size)
	if idx < 0 {
		p.oversized.Add(1)
		return make([]byte, size)
	}
	p.hits.Add(1)
	return p.slots[idx].get()[:size]
}

func (p *BucketedBytesPool) Put(buf []byte) {
	if buf == nil {
		return
	}
	c := cap(buf)
	for i, size := range p.bucketSizes {
		if c == size {
			p.slots[i].put(buf[:size])
			return
		}
	}
	p.misses.Add(1)
}

func (p *BucketedBytesPool) Stats() BucketedBytesPoolStats {
	return BucketedBytesPoolStats{
		Hits:      p.hits.Load(),
		Misses:    p.misses.Load(),
		Oversized: p.oversized.Load(),
	}
}

func (p *BucketedBytesPool) BucketSizes() []int {
	out := make([]int, len(p.bucketSizes))
	copy(out, p.bucketSizes)
	return out
}

func (p *BucketedBytesPool) findBucketIndex(size int) int {
	for i, bucketSize := range p.bucketSizes {
		if size <= bucketSize {
			return i
		}
	}
	return -1
}

func computeBucketSizes(base int) []int {
	if base <= 0 {
		base = defaultBucketBase
	}
	candidates := []int{
		base / 4,
		base / 2,
		base,
		base * 2,
		base * 4,
	}
	seen := make(map[int]struct{}, defaultBucketCount)
	buckets := make([]int, 0, defaultBucketCount)
	for _, candidate := range candidates {
		candidate = clamp(candidate, minBucketSize, maxBucketSize)
		candidate = align(candidate, bucketAlignBytes)
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		buckets = append(buckets, candidate)
	}
	sort.Ints(buckets)
	return buckets
}

func clamp(v, minV, maxV int) int {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

func align(v, alignment int) int {
	if alignment <= 0 {
		return v
	}
	if v%alignment == 0 {
		return v
	}
	return ((v / alignment) + 1) * alignment
}
