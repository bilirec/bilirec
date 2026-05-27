package rest

import (
	"crypto/subtle"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type loginRequest struct {
	User string `json:"user" form:"user"`
	Pass string `json:"pass" form:"pass"`
}

// Login
// @Summary Login and get JWT token
// @Tags auth
// @Accept json
// @Produce json
// @Param credentials body loginRequest true "Login credentials"
// @Success 200 {string} string "Login successful; JWT token is set in cookie"
// @Failure 400 {string} string "Bad request"
// @Failure 401 {string} string "Unauthorized"
// @Router /login [post]
func loginHandler(cfg *config.Config) fiber.Handler {
	return func(c fiber.Ctx) error {

		var req loginRequest
		if err := c.Bind().All(&req); err != nil {
			return fiber.ErrBadRequest
		}

		user := req.User
		pass := req.Pass

		role := RetrieveRole(cfg, user, pass)
		if role == "" {
			return fiber.ErrUnauthorized
		}

		expire := time.Now().Add(time.Hour * 72)

		claims := jwt.MapClaims{
			"name": user,
			"iat":  time.Now().Unix(),
			"exp":  expire.Unix(),
			"iss":  "bilirec",
			"role": role,
		}

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		t, err := token.SignedString([]byte(cfg.JwtSecret))
		if err != nil {
			return fiber.ErrInternalServerError
		}

		c.Cookie(&fiber.Cookie{
			Expires:  expire,
			Name:     jwtTokenKey,
			Value:    t,
			Path:     "/",
			HTTPOnly: cfg.ProductionMode,
			Secure:   cfg.ProductionMode,
			SameSite: "None",
		})

		return c.JSON(fiber.Map{
			"user": user,
			"role": role,
		})
	}
}

// Logout
// @Summary Logout and clear JWT cookie
// @Tags auth
// @Accept json
// @Produce json
// @Success 200 {string} string "Logout successful"
// @Router /logout [post]
func logoutHandler(cfg *config.Config) fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Cookie(&fiber.Cookie{
			Name:     jwtTokenKey,
			Value:    "",
			Path:     "/",
			Expires:  time.Now().Add(-1 * time.Hour), // in the past
			HTTPOnly: cfg.ProductionMode,
			Secure:   cfg.ProductionMode,
			SameSite: "None",
		})
		return c.SendStatus(fiber.StatusOK)
	}
}

func compareUsernameAndPassword(cfg *config.Config, user, pass string) bool {
	return subtle.ConstantTimeCompare([]byte(user), []byte(cfg.Username)) == 1 && bcrypt.CompareHashAndPassword([]byte(cfg.PasswordHash), []byte(pass)) == nil
}
