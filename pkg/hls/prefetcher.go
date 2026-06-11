package hls

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
)

// SegmentPrefetcher prefetches segments while preserving consumer pull order.
type SegmentPrefetcher struct {
	ctx      context.Context
	client   *resty.Client
	resolver *URLResolver
	attempts int
	delay    time.Duration
	started  map[int64]chan SegmentFetchResult
	mu       sync.Mutex
	sem      chan struct{}
}

// NewSegmentPrefetcher creates a prefetcher using fixed retry settings.
func NewSegmentPrefetcher(ctx context.Context, client *resty.Client, resolver *URLResolver, attempts int, delay time.Duration, maxConcurrent int) *SegmentPrefetcher {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &SegmentPrefetcher{
		ctx:      ctx,
		client:   client,
		resolver: resolver,
		attempts: attempts,
		delay:    delay,
		started:  make(map[int64]chan SegmentFetchResult),
		sem:      make(chan struct{}, maxConcurrent),
	}
}

// Start ensures prefetch for seq has started.
func (p *SegmentPrefetcher) Start(seq int64, segmentURI string) {
	p.mu.Lock()
	if _, exists := p.started[seq]; exists {
		p.mu.Unlock()
		return
	}

	resultCh := make(chan SegmentFetchResult, 1)
	p.started[seq] = resultCh
	p.mu.Unlock()

	go func() {
		select {
		case p.sem <- struct{}{}:
		case <-p.ctx.Done():
			resultCh <- SegmentFetchResult{Err: p.ctx.Err()}
			return
		}
		defer func() {
			<-p.sem
		}()

		segmentURL, err := p.resolver.Resolve(segmentURI)
		if err != nil {
			resultCh <- SegmentFetchResult{Err: fmt.Errorf("解析分片 URL %q 失败：%w", segmentURI, err)}
			return
		}

		data, err := FetchSegmentWithRetry(p.ctx, p.client, segmentURL, p.attempts, p.delay)
		resultCh <- SegmentFetchResult{Data: data, Err: err}
	}()
}

// Wait waits for seq prefetch completion and returns its result.
func (p *SegmentPrefetcher) Wait(seq int64, segmentURI string) ([]byte, error) {
	p.Start(seq, segmentURI)
	p.mu.Lock()
	resultCh, ok := p.started[seq]
	if !ok {
		p.mu.Unlock()
		return nil, fmt.Errorf("未找到 seq=%d 的预取结果通道", seq)
	}
	delete(p.started, seq)
	p.mu.Unlock()

	select {
	case result := <-resultCh:
		return result.Data, result.Err
	case <-p.ctx.Done():
		return nil, p.ctx.Err()
	}
}
