package sink

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/bilirec/bilirec/pkg/backoff"
)

const (
	defaultVLogsTimeout = 10 * time.Second
	vlogsRetryBaseDelay = 100 * time.Millisecond
	vlogsRetryMaxDelay   = time.Second
)

type VLogsHTTPTransportOptions struct {
	URL       string
	AccountID string
	ProjectID string
	Timeout   time.Duration
	RetryMax  int
	OnFailed  func()
	OnRetry   func()
}

type VLogsHTTPTransport struct {
	url       string
	accountID string
	projectID string
	client    *http.Client
	retryMax  int
	onFailed  func()
	onRetry   func()
}

func NewVLogsHTTPTransport(opts VLogsHTTPTransportOptions) *VLogsHTTPTransport {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultVLogsTimeout
	}
	retryMax := opts.RetryMax
	if retryMax < 0 {
		retryMax = 0
	}

	return &VLogsHTTPTransport{
		url:       opts.URL,
		accountID: opts.AccountID,
		projectID: opts.ProjectID,
		client:    &http.Client{Timeout: timeout},
		retryMax:  retryMax,
		onFailed:  opts.OnFailed,
		onRetry:   opts.OnRetry,
	}
}

func (t *VLogsHTTPTransport) Consume(batch []byte) error {
	if len(batch) == 0 {
		return nil
	}
	bo := backoff.NewExpotential(vlogsRetryBaseDelay, 2, vlogsRetryMaxDelay)
	for attempt := 0; ; attempt++ {
		retry, err := t.post(batch)
		if !retry || attempt >= t.retryMax {
			return err
		}
		if t.onRetry != nil {
			t.onRetry()
		}
		time.Sleep(bo.Next())
	}
}

func (t *VLogsHTTPTransport) Close(context.Context) error {
	return nil
}

func (t *VLogsHTTPTransport) post(body []byte) (bool, error) {
	req, err := http.NewRequest(http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger: victorialogs request: %v\n", err)
		return false, err
	}
	req.Header.Set("Content-Type", "application/stream+json")
	if t.accountID != "" {
		req.Header.Set("AccountID", t.accountID)
	}
	if t.projectID != "" {
		req.Header.Set("ProjectID", t.projectID)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger: victorialogs post: %v\n", err)
		if t.onFailed != nil {
			t.onFailed()
		}
		return true, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false, nil
	}

	fmt.Fprintf(os.Stderr, "logger: victorialogs post: HTTP %s\n", resp.Status)
	if t.onFailed != nil {
		t.onFailed()
	}

	retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError
	return retry, fmt.Errorf("logger: victorialogs post: HTTP %s", resp.Status)
}
