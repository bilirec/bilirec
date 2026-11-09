package rest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx/fxtest"
)

func newRootTestApp(t *testing.T) *fiber.App {
	t.Helper()
	return provider(fxtest.NewLifecycle(t), &config.Config{})
}

func doRootRequest(t *testing.T, app *fiber.App, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s: read body: %v", path, err)
	}
	return resp.StatusCode, string(body)
}

func TestRootFallback_NoSwagger(t *testing.T) {
	app := newRootTestApp(t)

	status, body := doRootRequest(t, app, "/")
	if status != http.StatusOK {
		t.Fatalf("GET /: got %d, want 200", status)
	}
	if body != "OK" {
		t.Fatalf("GET /: got body %q, want %q", body, "OK")
	}
}

func TestRootFallback_VersionStillWorks(t *testing.T) {
	app := newRootTestApp(t)

	status, _ := doRootRequest(t, app, "/version")
	if status != http.StatusOK {
		t.Fatalf("GET /version: got %d, want 200", status)
	}
}

func TestRootFallback_UnknownPathStill404(t *testing.T) {
	app := newRootTestApp(t)

	status, _ := doRootRequest(t, app, "/not-a-route")
	if status != http.StatusNotFound {
		t.Fatalf("GET /not-a-route: got %d, want 404", status)
	}
}
