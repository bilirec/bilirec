package preview

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	pvs "github.com/bilirec/bilirec/internal/services/preview"
	"github.com/bilirec/bilirec/pkg/pipeline"
	"github.com/gofiber/fiber/v3"
)

func TestMediaQueryRangeSendsBody(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.flv")
	payload := bytes.Repeat([]byte{0x12}, 64)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	svc := pvs.NewService()
	pipe := pipeline.New(svc.Tap(1, path, ".flv"))
	if err := pipe.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pipe.Close)

	app := fiber.New()
	NewController(app, svc, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/preview/rooms/1/media?offset=8&length=16", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, payload[8:24]) {
		t.Fatalf("body=%x want %x", body, payload[8:24])
	}
}
