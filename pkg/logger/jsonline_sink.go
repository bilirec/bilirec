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
)

const (
	defaultJSONLineBatchBytes    = 64 << 10 // 64 KiB
	defaultJSONLineFlushInterval = time.Second
	defaultJSONLineBufferSize    = 4096
	defaultJSONLineTimeout       = 10 * time.Second
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
	}
	s.wg.Add(1)
	go s.run()
	return s
}

func (s *JSONLineSink) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	line := append([]byte(nil), p...)
	select {
	case s.ch <- line:
	default:
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
		body := append([]byte(nil), batch.Bytes()...)
		batch.Reset()
		s.post(body)
	}
	drainPending := func() {
		for {
			select {
			case line, ok := <-s.ch:
				if !ok {
					return
				}
				batch.Write(line)
			default:
				return
			}
		}
	}

	for {
		select {
		case line, ok := <-s.ch:
			if !ok {
				drainPending()
				flush()
				return
			}
			batch.Write(line)
			if batch.Len() >= s.batchBytes {
				flush()
			}
		case <-ticker.C:
			drainPending()
			flush()
		case <-s.flushCh:
			drainPending()
			flush()
		}
	}
}

func (s *JSONLineSink) post(body []byte) {
	if len(body) == 0 {
		return
	}
	req, err := http.NewRequest(http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger: victorialogs request: %v\n", err)
		return
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
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "logger: victorialogs post: HTTP %s\n", resp.Status)
	}
}
