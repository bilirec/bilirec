package notify

import (
	"errors"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	ns "github.com/bilirec/bilirec/internal/services/notify"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/sse"
	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("controller", "notify")

type Controller struct {
	notifySvc  *ns.Service
	sseHandler fiber.Handler
}

func NewController(app *fiber.App, notifySvc *ns.Service) *Controller {
	c := &Controller{notifySvc: notifySvc}
	group := app.Group("/notify")
	group.Get("/public-key", c.webPushPublicKey)
	group.Post("/subscribe", c.webPushSubscribe)
	group.Delete("/subscribe", c.webPushUnsubscribe)
	group.Get("/sse", c.sse)
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
	if state := c.notifySvc.WebPushServiceState(); !state.Enabled {
		return ctx.JSON(WebPushPublicKeyResponse{Enabled: false})
	} else {
		return ctx.JSON(WebPushPublicKeyResponse{
			Enabled:   true,
			PublicKey: state.PublicKey,
		})
	}
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
	if !c.notifySvc.WebPushServiceState().Enabled {
		return fiber.NewError(fiber.StatusServiceUnavailable, "Web Push 尚未启用")
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

	if err := c.notifySvc.AddWebPushSubscription(sub); err != nil {
		logger.Errorf("添加 Web Push 订阅失败：%v", err)
		return fiber.NewError(fiber.StatusBadRequest, "新增 Web Push 订阅失败")
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
		return fiber.NewError(fiber.StatusBadRequest, "endpoint 为必填项")
	}

	if err := c.notifySvc.RemoveWebPushSubscription(endpoint); err != nil {
		logger.Warnf("移除 Web Push 订阅失败：%s", endpoint)
		return fiber.NewError(fiber.StatusNotFound, "取消 Web Push 订阅失败")
	}

	return ctx.SendStatus(fiber.StatusNoContent)
}

// @Summary Subscribe notification stream via SSE
// @Description Subscribe to live notification events via Server-Sent Events with query token
// @Tags notify
// @Param token query string true "SSE access token"
// @Produce text/event-stream
// @Success 200 {string} string "SSE stream opened"
// @Failure 401 {string} string "Unauthorized"
// @Failure 503 {string} string "SSE disabled"
// @Router /notify/sse [get]
func (c *Controller) sse(ctx fiber.Ctx) error {
	clientID, ch, err := c.notifySvc.SubscribeSSE(strings.TrimSpace(ctx.Query("token")))
	if err != nil {
		return c.mapSSEError(err)
	}
	ctx.Locals("sseClientID", clientID)
	ctx.Locals("sseChan", ch)
	return c.sseHandle(ctx)
}

func (c *Controller) sseHandle(ctx fiber.Ctx) error {
	if c.sseHandler == nil {
		c.sseHandler = sse.New(sse.Config{
			HeartbeatInterval: 25 * time.Second,
			Handler: func(ctx fiber.Ctx, stream *sse.Stream) error {
				clientID := ctx.Locals("sseClientID").(uint64)
				ch := ctx.Locals("sseChan").(<-chan []byte)

				defer c.notifySvc.UnsubscribeSSE(clientID)
				_ = stream.Comment("connected")

				for {
					select {
					case payload, ok := <-ch:
						if !ok {
							return nil // Channel closed, end stream
						}
						if err := stream.Event(sse.Event{Data: payload}); err != nil {
							return err
						}
					case <-stream.Done():
						return stream.Err() // Client disconnected, end stream
					}
				}
			},
		})
	}
	return c.sseHandler(ctx)
}

func (c *Controller) mapSSEError(err error) error {
	switch {
	case errors.Is(err, ns.ErrSSEDisabled):
		return fiber.NewError(fiber.StatusServiceUnavailable, err.Error())
	case errors.Is(err, ns.ErrSSETokenMissing), errors.Is(err, ns.ErrSSETokenInvalid):
		return fiber.NewError(fiber.StatusUnauthorized, err.Error())
	default:
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
}
