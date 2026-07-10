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
	readBody SegmentBodyReader
	release  BytesReleaser
	started  map[int64]chan SegmentFetchResult
	mu       sync.Mutex
	sem      chan struct{}
}

// NewSegmentPrefetcher creates a prefetcher using fixed retry settings.
// release may be nil; when set, abandoned / cancel-orphaned segment bodies are returned to the pool.
func NewSegmentPrefetcher(
	ctx context.Context,
	client *resty.Client,
	resolver *URLResolver,
	attempts int,
	delay time.Duration,
	maxConcurrent int,
	readBody SegmentBodyReader,
	release BytesReleaser,
) *SegmentPrefetcher {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	if release == nil {
		release = func([]byte) {}
	}
	return &SegmentPrefetcher{
		ctx:      ctx,
		client:   client,
		resolver: resolver,
		attempts: attempts,
		delay:    delay,
		readBody: readBody,
		release:  release,
		started:  make(map[int64]chan SegmentFetchResult),
		sem:      make(chan struct{}, maxConcurrent),
	}
}

func (p *SegmentPrefetcher) releaseData(data []byte) {
	if len(data) == 0 && cap(data) == 0 {
		return
	}
	p.release(data)
}

// Abandon drops all in-flight and completed-but-unconsumed prefetch results,
// releasing any owned segment bodies. Safe to call when replacing the prefetcher.
func (p *SegmentPrefetcher) Abandon() {
	if p == nil {
		return
	}
	p.mu.Lock()
	pending := p.started
	p.started = make(map[int64]chan SegmentFetchResult)
	p.mu.Unlock()
	for _, resultCh := range pending {
		ch := resultCh
		go func() {
			result := <-ch
			p.releaseData(result.Data)
		}()
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

		data, err := FetchSegmentWithRetryReader(p.ctx, p.client, segmentURL, p.attempts, p.delay, p.readBody)
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
		// Fetch goroutine may still complete; drain so pool buffers are released.
		go func() {
			result := <-resultCh
			p.releaseData(result.Data)
		}()
		return nil, p.ctx.Err()
	}
}
