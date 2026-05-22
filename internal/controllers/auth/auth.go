package auth

import (
	"errors"

	"github.com/eric2788/bilirec/internal/modules/bilibili"
	"github.com/eric2788/bilirec/internal/modules/rest"
	"github.com/eric2788/bilirec/utils"
	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("controller", "auth")

type Controller struct {
	client *bilibili.Client
}

func NewController(app *fiber.App, client *bilibili.Client) *Controller {
	rc := &Controller{
		client: client,
	}

	auth := app.Group("/auth/bilibili")
	auth.Get("/status", rc.getStatus)
	auth.Post("/init", rest.AdminOnly, rc.initLogin)

	return rc
}

// @Summary Get bilibili authentication status
// @Description Get current bilibili authentication state and QR session info
// @Tags auth
// @Security BearerAuth
// @Accept json
// @Produce json
// @Success 200 {object} StatusResponse "Current auth status"
// @Router /auth/bilibili/status [get]
func (r *Controller) getStatus(ctx fiber.Ctx) error {
	session := r.client.GetSession()

	resp := StatusResponse{
		Authenticated: session.Account != nil,
		State:         string(session.State),
	}

	if session.Account != nil {
		resp.Account = &AccountInfo{
			Mid:   session.Account.Mid,
			Uname: session.Account.Uname,
		}
	}

	if session.QrcodeURL != "" {
		resp.QR = &QRInfo{
			URL: session.QrcodeURL,
		}
	}

	if session.Error != nil {
		resp.LastError = session.Error.Error()
	}

	return ctx.JSON(resp)
}

// @Summary Initiate bilibili QR login
// @Description Start a new QR code login session (only available in controller mode). Allows switching accounts even if already authenticated.
// @Tags auth
// @Security BearerAuth
// @Accept json
// @Produce json
// @Success 201 {object} InitLoginResponse "QR session created"
// @Failure 400 {object} InitLoginResponse "Not in controller mode"
// @Failure 500 {object} InitLoginResponse "Failed to generate QR code or other server error"
// @Router /auth/bilibili/init [post]
func (r *Controller) initLogin(ctx fiber.Ctx) error {
	qrResp, err := r.client.InitQRLogin()
	if err != nil {
		logger.Warnf("failed to init QR login: %v", err)
		return ctx.Status(
			utils.Ternary(
				errors.Is(err, bilibili.ErrNotControllerMode),
				400,
				500,
			)).
			JSON(InitLoginResponse{
				Error: err.Error(),
			})
	}

	return ctx.Status(fiber.StatusCreated).JSON(InitLoginResponse{
		QR: &QRInfo{
			URL: qrResp.Url,
		},
	})
}
