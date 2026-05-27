package notify

import (
	"errors"
	"testing"

	ns "github.com/bilirec/bilirec/internal/services/notify"
	"github.com/gofiber/fiber/v3"
)

func TestController_mapSSEError(t *testing.T) {
	c := &Controller{}

	tests := []struct {
		name       string
		err        error
		statusCode int
	}{
		{name: "disabled", err: ns.ErrSSEDisabled, statusCode: fiber.StatusServiceUnavailable},
		{name: "missing token", err: ns.ErrSSETokenMissing, statusCode: fiber.StatusUnauthorized},
		{name: "invalid token", err: ns.ErrSSETokenInvalid, statusCode: fiber.StatusUnauthorized},
		{name: "internal", err: errors.New("other"), statusCode: fiber.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := c.mapSSEError(tt.err)
			fiberErr, ok := err.(*fiber.Error)
			if !ok {
				t.Fatalf("expected fiber error but got %T", err)
			}
			if fiberErr.Code != tt.statusCode {
				t.Fatalf("expected status %d but got %d", tt.statusCode, fiberErr.Code)
			}
		})
	}
}
