package preview

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pvs "github.com/bilirec/bilirec/internal/services/preview"
	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/pipeline"
	"github.com/gofiber/fiber/v3"
)

func TestMediaStreamsFanout(t *testing.T) {
	t.Parallel()

	svc := pvs.NewService()
	pipe := pipeline.New(svc.Tap(1, "a.ts", ".ts"))
	if err := pipe.Open(context.Background()); err != nil {
		t.Fatal(err)
	}

	chunk := bytes.Repeat([]byte{0x47}, 188)
	app := fiber.New()
	NewController(app, svc, nil, nil)

	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _ = pipe.Process(context.Background(), chunk)
		pipe.Close()
	}()

	req := httptest.NewRequest(http.MethodGet, "/preview/rooms/1/media", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if ct := resp.Header.Get(fiber.HeaderContentType); ct != "video/mp2t" {
		t.Fatalf("content-type=%s", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, chunk) {
		t.Fatalf("len=%d", len(body))
	}
}

func TestMediaRequiresRecording(t *testing.T) {
	t.Parallel()
	app := fiber.New()
	NewController(app, pvs.NewService(), nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/preview/rooms/1/media", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestFLVBootstrapUsedInHTTP(t *testing.T) {
	t.Parallel()
	svc := pvs.NewService()
	pipe := pipeline.New(svc.Tap(2, "a.flv", ".flv"))
	if err := pipe.Open(context.Background()); err != nil {
		t.Fatal(err)
	}

	seq := flv.NewTagBytes(flv.TagTypeVideo, []byte{0x17, 0x00, 0x00, 0x00, 0x00, 0x01})
	key := flv.NewTagBytes(flv.TagTypeVideo, []byte{0x17, 0x01, 0x00, 0x00, 0x00, 'K'})
	flv.SetTagTimestamp(key, 5_000)
	var head bytes.Buffer
	head.Write(flv.NewFileHeaderBytes())
	head.Write(seq)
	if _, err := pipe.Process(context.Background(), head.Bytes()); err != nil {
		t.Fatal(err)
	}

	app := fiber.New()
	NewController(app, svc, nil, nil)
	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _ = pipe.Process(context.Background(), key)
		pipe.Close()
	}()

	req := httptest.NewRequest(http.MethodGet, "/preview/rooms/2/media", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(body, flv.NewFileHeaderBytes()) {
		t.Fatal("missing header")
	}
	if !bytes.Contains(body, []byte{'K'}) {
		t.Fatal("missing keyframe")
	}
}
