package utils

import (
	"os"

	"github.com/joho/godotenv"
)

// LoadDotEnvLocal loads .env.local from common relative locations.
// Existing environment variables are preserved and not overwritten.
func LoadDotEnvLocal() {
	candidates := []string{
		".env.local",
		"../.env.local",
		"../../.env.local",
		"../../../.env.local",
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		m, err := godotenv.Read(p)
		if err != nil {
			continue
		}
		for k, v := range m {
			if os.Getenv(k) == "" {
				_ = os.Setenv(k, v)
			}
		}
	}
}
