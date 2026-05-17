package rest

import (
	"crypto/subtle"

	"github.com/eric2788/bilirec/internal/modules/config"
	"github.com/eric2788/bilirec/pkg/ds"
	"github.com/eric2788/bilirec/utils"
	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"
)

type Role string

const (
	RoleAdmin  Role = "admin"
	RoleViewer Role = "viewer"
)

var (
	AdminOnly = RequiredRoles(RoleAdmin)
)

func RetrieveRole(cfg *config.Config, username, password string) string {
	if timeCompare(username, cfg.Username) && hashCompare(cfg.PasswordHash, password) {
		return string(RoleAdmin)
	} else if timeCompare(username, cfg.ViewerUsername) && hashCompare(cfg.ViewerPasswordHash, password) {
		return string(RoleViewer)
	}
	return ""
}

func timeCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func hashCompare(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func RequiredRoles(roles ...Role) fiber.Handler {
	allowedRoles := ds.NewSyncedSet[Role]()
	for _, role := range roles {
		allowedRoles.Add(role)
	}
	return func(c fiber.Ctx) error {

		if !config.ReadOnly.RestAuthEnabled() {
			return c.Next()
		}

		claims := utils.ToJwtClaims(c)
		if claims == nil {
			return fiber.NewError(403, "没有权限")
		}

		role, ok := utils.GetClaimString(claims, "role")
		if !ok || !allowedRoles.Contains(Role(role)) {
			return fiber.NewError(403, "没有权限")
		}

		return c.Next()
	}
}
