package pool

import (
	"sort"
	"sync"
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
	pools       []sync.Pool

	hits      atomic.Uint64
	misses    atomic.Uint64
	oversized atomic.Uint64
}

type BucketedBytesPoolStats struct {
	Hits      uint64
	Misses    uint64
	Oversized uint64
}

func NewBucketedBytesPool(baseSize int) *BucketedBytesPool {
	buckets := computeBucketSizes(baseSize)
	p := &BucketedBytesPool{
		bucketSizes: buckets,
		pools:       make([]sync.Pool, len(buckets)),
	}
	for i, bucketSize := range buckets {
		size := bucketSize
		p.pools[i] = sync.Pool{
			New: func() any {
				return make([]byte, size)
			},
		}
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
	return p.pools[idx].Get().([]byte)[:size]
}

func (p *BucketedBytesPool) Put(buf []byte) {
	if buf == nil {
		return
	}
	c := cap(buf)
	for i, size := range p.bucketSizes {
		if c == size {
			p.pools[i].Put(buf[:size])
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
