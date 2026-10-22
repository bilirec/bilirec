package preview

import (
	"bytes"
	"errors"
	"strconv"
	"time"

	pvs "github.com/bilirec/bilirec/internal/services/preview"
	"github.com/bilirec/bilirec/pkg/logger"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/sse"
)

var log = logger.Named("preview-http")

type Controller struct {
	svc     *pvs.Service
	sseOnce fiber.Handler
}

func NewController(app *fiber.App, svc *pvs.Service) *Controller {
	c := &Controller{svc: svc}
	g := app.Group("/preview/rooms/:roomID")
	g.Get("/media", c.media)
	g.Get("/events", c.events)
	return c
}

func (c *Controller) media(ctx fiber.Ctx) error {
	roomID, err := parseRoomID(ctx)
	if err != nil {
		return err
	}
	media, err := c.svc.OpenMedia(roomID)
	if err != nil {
		return mapPreviewError(err)
	}
	disablePreviewHTTPCache(ctx)
	ctx.Set(fiber.HeaderContentType, media.MIME)
	return ctx.SendStream(media.Body)
}

func (c *Controller) events(ctx fiber.Ctx) error {
	roomID, err := parseRoomID(ctx)
	if err != nil {
		return err
	}
	sub, err := c.svc.Subscribe(roomID)
	if err != nil {
		return mapPreviewError(err)
	}
	disablePreviewHTTPCache(ctx)
	ctx.Locals("previewSub", sub)
	ctx.Locals("previewRoom", roomID)
	return c.sseHandle(ctx)
}

func (c *Controller) sseHandle(ctx fiber.Ctx) error {
	if c.sseOnce == nil {
		c.sseOnce = sse.New(sse.Config{
			HeartbeatInterval: 25 * time.Second,
			Handler: func(ctx fiber.Ctx, stream *sse.Stream) error {
				sub := ctx.Locals("previewSub").(pvs.EventSub)
				roomID := ctx.Locals("previewRoom").(int)
				defer sub.Leave()

				_ = stream.Comment("connected")
				c.sendCatchup(stream, roomID, sub)

				for {
					select {
					case payload, ok := <-sub.C:
						if !ok {
							return nil
						}
						line := bytes.TrimRight(payload, "\r\n")
						if len(line) == 0 {
							continue
						}
						if err := stream.Event(sse.Event{Data: sub.Rebase(line)}); err != nil {
							return err
						}
					case <-stream.Done():
						return stream.Err()
					}
				}
			},
		})
	}
	return c.sseOnce(ctx)
}

func (c *Controller) sendCatchup(stream *sse.Stream, roomID int, sub pvs.EventSub) {
	lines, err := c.svc.Catchup(roomID)
	if err != nil {
		log.Warnf("房间 %d 预览弹幕回放读取失败：%v", roomID, err)
		return
	}
	for _, line := range lines {
		if err := stream.Event(sse.Event{Data: sub.Rebase(line)}); err != nil {
			return
		}
	}
}

func disablePreviewHTTPCache(ctx fiber.Ctx) {
	ctx.Set(fiber.HeaderCacheControl, "no-store, no-cache, must-revalidate")
	ctx.Set(fiber.HeaderPragma, "no-cache")
	ctx.Set(fiber.HeaderExpires, "0")
	ctx.Set("X-Accel-Buffering", "no")
}

func parseRoomID(ctx fiber.Ctx) (int, error) {
	roomID, err := strconv.Atoi(ctx.Params("roomID"))
	if err != nil || roomID <= 0 {
		return 0, fiber.NewError(fiber.StatusBadRequest, "无效的房间 ID")
	}
	return roomID, nil
}

func mapPreviewError(err error) error {
	if errors.Is(err, pvs.ErrNotRecording) {
		return fiber.NewError(fiber.StatusConflict, err.Error())
	}
	return fiber.NewError(fiber.StatusInternalServerError, err.Error())
}
