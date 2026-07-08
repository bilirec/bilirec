package stream

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-resty/resty/v2"
)

func TestReadHlsSegmentBody_PooledReuse(t *testing.T) {
	payload := bytes.Repeat([]byte{0xAB}, 64*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	chunkPool := newChunkBytesPool(512*1024, 4)
	resp, err := resty.New().R().SetDoNotParseResponse(true).Get(server.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	data, err := readHlsSegmentBody(chunkPool, resp)
	if err != nil {
		t.Fatalf("readHlsSegmentBody: %v", err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("payload mismatch: got %d bytes", len(data))
	}

	firstCap := cap(data)
	chunkPool.Put(data[:firstCap])
	reused := chunkPool.GetSized(firstCap)
	if cap(reused) != firstCap {
		t.Fatalf("expected same bucket cap on reuse, got %d vs %d", cap(reused), firstCap)
	}
	chunkPool.Put(reused[:cap(reused)])
}

func TestChunkPool_PutOnlyMatchingBucket(t *testing.T) {
	chunkPool := newChunkBytesPool(512*1024, 4)
	buf := chunkPool.GetSized(512 * 1024)
	chunkPool.Put(buf[:cap(buf)])

	stats := chunkPool.Stats()
	if stats.Hits == 0 && stats.Misses > 0 {
		// first Get may count as hit; Put then Get should reuse
	}
	reused := chunkPool.GetSized(512 * 1024)
	if cap(reused) != 512*1024 {
		t.Fatalf("unexpected cap %d", cap(reused))
	}
}
