package bilibili

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bilirec/bilirec/pkg/fallback"
	bili "github.com/CuteReimu/bilibili/v2"
	"github.com/go-resty/resty/v2"
)

func TestIsRiskControl(t *testing.T) {
	tests := []struct {
		code int
		want bool
	}{
		{0, false},
		{1, false},
		{400, false},
		{404, false},
		{500, false},
		{502, false},
		{-352, true},
		{1001, false},
	}
	for _, tc := range tests {
		if got := isRiskControl(tc.code); got != tc.want {
			t.Fatalf("isRiskControl(%d) = %v, want %v", tc.code, got, tc.want)
		}
	}
}

func TestParseAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":-352,"message":"风控"}`))
	}))
	defer srv.Close()

	resp, err := resty.New().R().Get(srv.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	apiErr, err := ParseAPIError(resp)
	if err != nil {
		t.Fatalf("ParseAPIError failed: %v", err)
	}
	if apiErr.Code != -352 || apiErr.Message != "风控" {
		t.Fatalf("unexpected api error: %+v", apiErr)
	}
	if apiErr.HTTPStatus != http.StatusOK {
		t.Fatalf("http status = %d, want 200", apiErr.HTTPStatus)
	}
}

func TestLiveInterpret(t *testing.T) {
	makeResp := func(body string) *resty.Response {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		resp, err := resty.New().R().Get(srv.URL)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		return resp
	}

	if got := liveInterpret(context.Background(), makeResp(`{"code":0,"message":"0"}`), nil); got != fallback.DecisionOK {
		t.Fatalf("code 0: got %v", got)
	}
	if got := liveInterpret(context.Background(), makeResp(`{"code":-352,"message":"风控"}`), nil); got != fallback.DecisionFallback {
		t.Fatalf("risk control: got %v", got)
	}
	if got := liveInterpret(context.Background(), makeResp(`{"code":404,"message":"nf"}`), nil); got != fallback.DecisionAbort {
		t.Fatalf("404: got %v", got)
	}
	if got := liveInterpret(context.Background(), nil, context.Canceled); got != fallback.DecisionAbort {
		t.Fatalf("transport err: got %v", got)
	}
}

func TestAsAPIError_fromBiliError(t *testing.T) {
	biliErr := bili.Error{Code: -352, Message: "风控"}
	apiErr, ok := AsAPIError(biliErr)
	if !ok {
		t.Fatal("expected AsAPIError to recognize bili.Error")
	}
	if apiErr.Code != -352 || apiErr.Message != "风控" {
		t.Fatalf("unexpected api error: %+v", apiErr)
	}
	if apiErr.HTTPStatus != 0 {
		t.Fatalf("http status should be 0 for bili.Error, got %d", apiErr.HTTPStatus)
	}
}

func TestIsErrRoomNotFound_APIError(t *testing.T) {
	err := &APIError{Code: 1, Message: "房间不存在"}
	if !IsErrRoomNotFound(err) {
		t.Fatal("expected room not found for api code 1")
	}
}
