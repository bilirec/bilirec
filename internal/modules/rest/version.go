package rest

import (
	"github.com/bilirec/bilirec/pkg/updatecheck"
	"github.com/gofiber/fiber/v3"
)

// VersionResponse is the JSON body for version endpoints.
type VersionResponse = updatecheck.Result

// GetVersion
// @Summary Get cached version check result
// @Tags version
// @Produce json
// @Success 200 {object} VersionResponse
// @Router /version [get]
func getVersionHandler() fiber.Handler {
	return func(c fiber.Ctx) error {
		return c.JSON(updatecheck.Cached())
	}
}

// PostVersionCheck
// @Summary Check for updates against GitHub releases
// @Tags version
// @Produce json
// @Success 200 {object} VersionResponse
// @Failure 400 {object} VersionResponse
// @Failure 502 {object} VersionResponse
// @Router /version/check [post]
func postVersionCheckHandler() fiber.Handler {
	return func(c fiber.Ctx) error {
		if updatecheck.Current() == "" {
			return c.Status(fiber.StatusBadRequest).JSON(VersionResponse{
				Current:   "",
				URL:       updatecheck.Cached().URL,
				Error:     "当前构建未嵌入版本号，无法检查更新",
				ErrorCode: updatecheck.ErrorCodeNoEmbeddedVersion,
			})
		}

		res, err := updatecheck.Check()
		if err != nil {
			updatecheck.LogFailure(res, err)
			return c.Status(fiber.StatusBadGateway).JSON(res)
		}
		return c.JSON(res)
	}
}
