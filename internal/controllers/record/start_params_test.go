package record

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestResolvedFromStartRecordingRequest_streamProfilesArray(t *testing.T) {
	arr := []string{"hls-ts"}
	merged := resolvedFromStartRecordingRequest(StartRecordingRequest{
		StreamProfiles: &arr,
		StreamProfile:  strPtr("http-flv"),
	})
	if merged.streamProfileRaw != "" {
		t.Fatal("stream_profile string should be ignored when array is set")
	}
	if len(merged.streamProfilesFromJSON) != 1 || merged.streamProfilesFromJSON[0] != "hls-ts" {
		t.Fatalf("unexpected profiles: %v", merged.streamProfilesFromJSON)
	}
}

func TestRecordStartOptionsFromResolved_durationSemantics(t *testing.T) {
	t.Run("not provided", func(t *testing.T) {
		opts, err := recordStartOptionsFromResolved(resolvedStartParams{durationMinutes: 0})
		if err != nil {
			t.Fatal(err)
		}
		if len(opts) != 1 {
			t.Fatalf("expected only stream options wrapper, got %d opts", len(opts))
		}
	})

	t.Run("unlimited", func(t *testing.T) {
		opts, err := recordStartOptionsFromResolved(resolvedStartParams{durationMinutes: -1})
		if err != nil {
			t.Fatal(err)
		}
		if len(opts) != 2 {
			t.Fatalf("expected duration + stream options, got %d", len(opts))
		}
	})

	t.Run("positive minutes", func(t *testing.T) {
		opts, err := recordStartOptionsFromResolved(resolvedStartParams{durationMinutes: 45})
		if err != nil {
			t.Fatal(err)
		}
		if len(opts) != 2 {
			t.Fatalf("expected duration + stream options, got %d", len(opts))
		}
	})
}

func TestRecordStartOptionsFromResolved_streamProfiles(t *testing.T) {
	t.Run("invalid array", func(t *testing.T) {
		_, err := recordStartOptionsFromResolved(resolvedStartParams{
			hasStreamProfile:       true,
			streamProfilesFromJSON: []string{"not-a-profile"},
		})
		if err == nil {
			t.Fatal("expected error for invalid stream_profiles")
		}
	})

	t.Run("valid array", func(t *testing.T) {
		opts, err := recordStartOptionsFromResolved(resolvedStartParams{
			hasStreamProfile:       true,
			streamProfilesFromJSON: []string{"http-flv", "hls-ts"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(opts) != 1 {
			t.Fatalf("expected stream options only, got %d", len(opts))
		}
	})
}

func TestParseStartRecordingParams_queryOnly(t *testing.T) {
	app := fiber.New()
	app.Post("/record/:roomID/start", func(c fiber.Ctx) error {
		resolved, err := parseStartRecordingParams(c)
		if err != nil {
			return err
		}
		if resolved.durationMinutes != 10 || !resolved.recordDanmaku || !resolved.hasStreamProfile {
			return fiber.NewError(fiber.StatusTeapot, "unexpected resolved params")
		}
		return c.SendStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/record/1/start?duration_minutes=10&record_danmaku=true&stream_profile=http-flv", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
}

func TestParseStartRecordingParams_invalidJSON(t *testing.T) {
	app := fiber.New()
	app.Post("/start", func(c fiber.Ctx) error {
		_, err := parseStartRecordingParams(c)
		return err
	})

	req := httptest.NewRequest(http.MethodPost, "/start", bytes.NewReader([]byte(`{`)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d want 400", resp.StatusCode)
	}
}

func TestParseStartRecordingParams_emptyJSONObjectIgnoresQuery(t *testing.T) {
	app := fiber.New()
	app.Post("/start", func(c fiber.Ctx) error {
		resolved, err := parseStartRecordingParams(c)
		if err != nil {
			return err
		}
		if resolved.recordDanmaku {
			return fiber.NewError(fiber.StatusTeapot, "query should be ignored when body is present")
		}
		return c.SendStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/start?record_danmaku=true", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
}

func TestParseStartRecordingParams_jsonIgnoresQuery(t *testing.T) {
	app := fiber.New()
	app.Post("/start", func(c fiber.Ctx) error {
		resolved, err := parseStartRecordingParams(c)
		if err != nil {
			return err
		}
		if resolved.durationMinutes != 5 {
			return fiber.NewError(fiber.StatusTeapot, "duration")
		}
		return c.SendStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/start?duration_minutes=60", bytes.NewReader([]byte(`{"duration_minutes":5}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestParseStartRecordingParams_invalidQueryQn(t *testing.T) {
	app := fiber.New()
	app.Post("/start", func(c fiber.Ctx) error {
		_, err := parseStartRecordingParams(c)
		return err
	})

	req := httptest.NewRequest(http.MethodPost, "/start?qn=abc", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d want 400", resp.StatusCode)
	}
}

func strPtr(v string) *string {
	return &v
}
