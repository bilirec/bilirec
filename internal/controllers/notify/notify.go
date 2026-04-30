package notify

import (
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"
	ns "github.com/eric2788/bilirec/internal/services/notify"
	"github.com/gofiber/fiber/v3"
)

type Controller struct {
	notifySvc *ns.Service
}

func NewController(app *fiber.App, notifySvc *ns.Service) *Controller {
	c := &Controller{notifySvc: notifySvc}
	group := app.Group("/notify")
	group.Get("/public-key", c.webPushPublicKey)
	group.Post("/subscribe", c.webPushSubscribe)
	group.Delete("/subscribe", c.webPushUnsubscribe)
	return c
}

// @Summary Get Web Push public key
// @Description Get VAPID public key used by frontend to create push subscription
// @Tags notify
// @Security BearerAuth
// @Produce json
// @Success 200 {object} WebPushPublicKeyResponse
// @Router /notify/public-key [get]
func (c *Controller) webPushPublicKey(ctx fiber.Ctx) error {
	if !c.notifySvc.WebPushEnabled() {
		return ctx.JSON(WebPushPublicKeyResponse{Enabled: false})
	}

	return ctx.JSON(WebPushPublicKeyResponse{
		Enabled:   true,
		PublicKey: c.notifySvc.WebPushPublicKey(),
	})
}

// @Summary Register Web Push subscription
// @Description Register browser push subscription for receiving live notifications
// @Tags notify
// @Security BearerAuth
// @Accept json
// @Param subscription body WebPushSubscriptionRequest true "Push subscription payload"
// @Success 201
// @Failure 400 {string} string "Bad request"
// @Failure 503 {string} string "Web Push not enabled"
// @Router /notify/subscribe [post]
func (c *Controller) webPushSubscribe(ctx fiber.Ctx) error {
	if !c.notifySvc.WebPushEnabled() {
		return fiber.NewError(fiber.StatusServiceUnavailable, "Web Push 尚未啟用")
	}

	var req WebPushSubscriptionRequest
	if err := ctx.Bind().Body(&req); err != nil {
		return fiber.ErrBadRequest
	}

	sub := webpush.Subscription{
		Endpoint: req.Endpoint,
		Keys: webpush.Keys{
			Auth:   req.Keys.Auth,
			P256dh: req.Keys.P256dh,
		},
	}

	if ok := c.notifySvc.AddWebPushSubscription(sub); !ok {
		return fiber.NewError(fiber.StatusBadRequest, "無效的 Web Push subscription")
	}

	return ctx.SendStatus(fiber.StatusCreated)
}

// @Summary Remove Web Push subscription
// @Description Remove browser push subscription by endpoint
// @Tags notify
// @Security BearerAuth
// @Accept json
// @Param endpoint query string false "Subscription endpoint"
// @Param request body WebPushUnsubscribeRequest false "Unsubscribe payload when endpoint is not set in query"
// @Success 204
// @Failure 400 {string} string "Bad request"
// @Failure 404 {string} string "Not found"
// @Router /notify/subscribe [delete]
func (c *Controller) webPushUnsubscribe(ctx fiber.Ctx) error {
	endpoint := strings.TrimSpace(ctx.Query("endpoint"))

	if endpoint == "" {
		var req WebPushUnsubscribeRequest
		if err := ctx.Bind().Body(&req); err != nil {
			return fiber.ErrBadRequest
		}
		endpoint = strings.TrimSpace(req.Endpoint)
	}

	if endpoint == "" {
		return fiber.NewError(fiber.StatusBadRequest, "endpoint 為必填")
	}

	if ok := c.notifySvc.RemoveWebPushSubscription(endpoint); !ok {
		return fiber.NewError(fiber.StatusNotFound, "找不到對應 subscription")
	}

	return ctx.SendStatus(fiber.StatusNoContent)
}
