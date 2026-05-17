package hls

import (
	"context"
	"fmt"
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
}

// NewSegmentPrefetcher creates a prefetcher using fixed retry settings.
func NewSegmentPrefetcher(ctx context.Context, client *resty.Client, resolver *URLResolver, attempts int, delay time.Duration) *SegmentPrefetcher {
	return &SegmentPrefetcher{
		ctx:      ctx,
		client:   client,
		resolver: resolver,
		attempts: attempts,
		delay:    delay,
		started:  make(map[int64]chan SegmentFetchResult),
	}
}

// Start ensures prefetch for seq has started.
func (p *SegmentPrefetcher) Start(seq int64, segmentURI string) {
	if _, exists := p.started[seq]; exists {
		return
	}

	resultCh := make(chan SegmentFetchResult, 1)
	p.started[seq] = resultCh

	go func() {
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
	resultCh, ok := p.started[seq]
	if !ok {
		return nil, fmt.Errorf("未找到 seq=%d 的预取结果通道", seq)
	}
	delete(p.started, seq)

	select {
	case result := <-resultCh:
		return result.Data, result.Err
	case <-p.ctx.Done():
		return nil, p.ctx.Err()
	}
}
