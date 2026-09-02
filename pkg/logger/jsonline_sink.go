package logger

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bilirec/bilirec/pkg/backoff"
	"github.com/bilirec/bilirec/pkg/pool"
)

const (
	defaultJSONLineBatchBytes    = 64 << 10 // 64 KiB
	defaultJSONLineFlushInterval = time.Second
	defaultJSONLineBufferSize    = 4096
	defaultJSONLineTimeout       = 10 * time.Second
	defaultJSONLineRetryMax      = 2
	jsonLineRetryBaseDelay       = 100 * time.Millisecond
	jsonLineRetryMaxDelay        = time.Second
)

var (
	jsonLinePool = pool.NewBytesSlicePool(512, 8<<10)
	jsonBodyPool = pool.NewBytesSlicePool(defaultJSONLineBatchBytes, 96<<10)
)

// JSONLineOptions configures the VictoriaLogs JSON stream sink.
type JSONLineOptions struct {
	URL           string
	StreamFields  string
	App           string
	Instance      string
	AccountID     string
	ProjectID     string
	Timeout       time.Duration
	RetryMax      int
	OnQueueBytes  func(int)
	OnDropped     func()
	OnFailed      func()
	OnRetry       func()
	BatchBytes    int
	FlushInterval time.Duration
	BufferSize    int
}

// JSONLineSink batches log lines and POSTs them to VictoriaLogs /insert/jsonline.
type JSONLineSink struct {
	url           string
	accountID     string
	projectID     string
	client        *http.Client
	ch            chan []byte
	flushCh       chan struct{}
	done          chan struct{}
	wg            sync.WaitGroup
	batchBytes    int
	flushInterval time.Duration
	retryMax      int
	onQueueBytes  func(int)
	onDropped     func()
	onFailed      func()
	onRetry       func()
	dropped       atomic.Uint64
	stopped       atomic.Bool
}

func newJSONLineSink(opts JSONLineOptions) *JSONLineSink {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultJSONLineTimeout
	}
	batchBytes := opts.BatchBytes
	if batchBytes <= 0 {
		batchBytes = defaultJSONLineBatchBytes
	}
	flushInterval := opts.FlushInterval
	if flushInterval <= 0 {
		flushInterval = defaultJSONLineFlushInterval
	}
	bufferSize := opts.BufferSize
	if bufferSize <= 0 {
		bufferSize = defaultJSONLineBufferSize
	}
	retryMax := opts.RetryMax
	if retryMax < 0 {
		retryMax = 0
	}
	if retryMax == 0 && opts.RetryMax == 0 {
		retryMax = defaultJSONLineRetryMax
	}

	s := &JSONLineSink{
		url:           buildInsertURL(opts.URL, opts.StreamFields),
		accountID:     opts.AccountID,
		projectID:     opts.ProjectID,
		client:        &http.Client{Timeout: timeout},
		ch:            make(chan []byte, bufferSize),
		flushCh:       make(chan struct{}, 1),
		done:          make(chan struct{}),
		batchBytes:    batchBytes,
		flushInterval: flushInterval,
		retryMax:      retryMax,
		onQueueBytes:  opts.OnQueueBytes,
		onDropped:     opts.OnDropped,
		onFailed:      opts.OnFailed,
		onRetry:       opts.OnRetry,
	}
	s.wg.Add(1)
	go s.run()
	return s
}

func (s *JSONLineSink) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	line := jsonLinePool.GetSized(len(p))
	copy(line, p)
	select {
	case s.ch <- line:
		if s.onQueueBytes != nil {
			s.onQueueBytes(len(line))
		}
	default:
		jsonLinePool.Put(line)
		if s.onDropped != nil {
			s.onDropped()
		}
		n := s.dropped.Add(1)
		if n == 1 || n%1000 == 0 {
			fmt.Fprintf(os.Stderr, "logger: victorialogs buffer full, dropped %d log lines\n", n)
		}
	}
	return len(p), nil
}

func (s *JSONLineSink) Sync() error {
	select {
	case s.flushCh <- struct{}{}:
	default:
	}
	return nil
}

func (s *JSONLineSink) Stop(ctx context.Context) error {
	if !s.stopped.CompareAndSwap(false, true) {
		return nil
	}
	close(s.ch)
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *JSONLineSink) run() {
	defer s.wg.Done()
	defer close(s.done)

	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()

	var batch bytes.Buffer
	flush := func() {
		if batch.Len() == 0 {
			return
		}
		body := jsonBodyPool.GetSized(batch.Len())
		copy(body, batch.Bytes())
		batch.Reset()
		s.postWithRetry(body)
		jsonBodyPool.Put(body)
	}
	appendLine := func(line []byte) {
		if s.onQueueBytes != nil {
			s.onQueueBytes(-len(line))
		}
		if batch.Len() > 0 && batch.Len()+len(line) > s.batchBytes {
			flush()
		}
		batch.Write(line)
		if batch.Len() >= s.batchBytes {
			flush()
		}
		jsonLinePool.Put(line)
	}
	drainPending := func() {
		for {
			select {
			case line, ok := <-s.ch:
				if !ok {
					return
				}
				appendLine(line)
			default:
				return
			}
		}
	}

	for {
		select {
		case line, ok := <-s.ch:
			if !ok {
				flush()
				return
			}
			appendLine(line)
		case <-ticker.C:
			drainPending()
			flush()
		case <-s.flushCh:
			drainPending()
			flush()
		}
	}
}

func (s *JSONLineSink) postWithRetry(body []byte) {
	bo := backoff.NewExpotential(jsonLineRetryBaseDelay, 2, jsonLineRetryMaxDelay)
	for attempt := 0; ; attempt++ {
		if !s.post(body) || attempt >= s.retryMax {
			return
		}
		if s.onRetry != nil {
			s.onRetry()
		}
		time.Sleep(bo.Next())
	}
}

func (s *JSONLineSink) post(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	req, err := http.NewRequest(http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger: victorialogs request: %v\n", err)
		return false
	}
	req.Header.Set("Content-Type", "application/stream+json")
	if s.accountID != "" {
		req.Header.Set("AccountID", s.accountID)
	}
	if s.projectID != "" {
		req.Header.Set("ProjectID", s.projectID)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger: victorialogs post: %v\n", err)
		if s.onFailed != nil {
			s.onFailed()
		}
		return true
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "logger: victorialogs post: HTTP %s\n", resp.Status)
		if s.onFailed != nil {
			s.onFailed()
		}
		return resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError
	}
	return false
}
