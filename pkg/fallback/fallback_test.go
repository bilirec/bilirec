package fallback

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/go-resty/resty/v2"
)

func TestClient_Do_FallbackOnInterpret(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if call == 1 {
			_, _ = w.Write([]byte(`{"code":-352,"message":"风控"}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"message":"0"}`))
	}))
	defer srv.Close()

	interpret := func(ctx context.Context, resp *resty.Response, err error) Decision {
		if err != nil {
			return DecisionAbort
		}
		body := resp.String()
		if body == `{"code":0,"message":"0"}` {
			return DecisionOK
		}
		if body == `{"code":-352,"message":"风控"}` {
			return DecisionFallback
		}
		return DecisionAbort
	}

	client := New(resty.New(), resty.New(), interpret)
	_, err := client.Do(context.Background(), func(req *resty.Request) (*resty.Response, error) {
		return req.Get(srv.URL)
	})
	if err != nil {
		t.Fatalf("Do() error: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestClient_Do_AbortWithoutFallback(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":404,"message":"not found"}`))
	}))
	defer srv.Close()

	interpret := func(ctx context.Context, resp *resty.Response, err error) Decision {
		return DecisionAbort
	}

	client := New(resty.New(), resty.New(), interpret)
	_, err := client.Do(context.Background(), func(req *resty.Request) (*resty.Response, error) {
		return req.Get(srv.URL)
	})
	if err != nil {
		t.Fatalf("expected nil transport error, got %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestClient_Do_TransportErrorAborts(t *testing.T) {
	interpret := func(ctx context.Context, resp *resty.Response, err error) Decision {
		if err != nil {
			return DecisionAbort
		}
		return DecisionOK
	}

	client := New(resty.New(), resty.New(), interpret)
	_, err := client.Do(context.Background(), func(req *resty.Request) (*resty.Response, error) {
		return req.Get("http://127.0.0.1:1")
	})
	if err == nil {
		t.Fatal("expected transport error")
	}
}
