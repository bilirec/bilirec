package rest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
)

func testCORSApp(cfg *config.Config) *fiber.App {
	app := fiber.New()
	app.Use(cors.New(restCORSConfig(cfg)))
	app.Get("/version", func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})
	return app
}

func TestCORS_PrivateNetworkPreflight(t *testing.T) {
	t.Parallel()

	frontend, err := url.Parse("https://app.bilirec.org")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		FrontendURL:    frontend,
		ProductionMode: false,
	}
	app := testCORSApp(cfg)

	req := httptest.NewRequest(
		http.MethodOptions,
		"/version",
		nil,
	)
	req.Header.Set("Origin", "https://app.bilirec.org")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Private-Network", "true")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.Header.Get("Access-Control-Allow-Private-Network") != "true" {
		t.Fatalf(
			"expected Access-Control-Allow-Private-Network: true, got %q",
			resp.Header.Get("Access-Control-Allow-Private-Network"),
		)
	}
}

func TestCORS_PrivateNetworkHeaderAbsentWithoutRequest(t *testing.T) {
	t.Parallel()

	frontend, err := url.Parse("https://app.bilirec.org")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		FrontendURL:    frontend,
		ProductionMode: false,
	}
	app := testCORSApp(cfg)

	req := httptest.NewRequest(
		http.MethodOptions,
		"/version",
		nil,
	)
	req.Header.Set("Origin", "https://app.bilirec.org")
	req.Header.Set("Access-Control-Request-Method", "GET")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if v := resp.Header.Get("Access-Control-Allow-Private-Network"); v != "" {
		t.Fatalf("expected no Access-Control-Allow-Private-Network header, got %q", v)
	}
}
