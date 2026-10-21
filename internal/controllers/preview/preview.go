package preview

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/bilirec/bilirec/internal/services/danmaku"
	pvs "github.com/bilirec/bilirec/internal/services/preview"
	"github.com/bilirec/bilirec/internal/services/recorder"
	"github.com/bilirec/bilirec/pkg/logger"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/sse"
)

var log = logger.Named("preview-http")

type Controller struct {
	svc     *pvs.Service
	rec     *recorder.Service
	dm      *danmaku.Service
	sseOnce fiber.Handler
}

func NewController(app *fiber.App, svc *pvs.Service, rec *recorder.Service, dm *danmaku.Service) *Controller {
	c := &Controller{svc: svc, rec: rec, dm: dm}
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
	ctx.Set(fiber.HeaderContentType, media.MIME)
	if media.Status != 0 {
		ctx.Status(media.Status)
	}
	return ctx.SendStream(media.Body)
}

func (c *Controller) events(ctx fiber.Ctx) error {
	roomID, err := parseRoomID(ctx)
	if err != nil {
		return err
	}
	ch, leave, err := c.svc.Subscribe(roomID)
	if err != nil {
		return mapPreviewError(err)
	}
	ctx.Locals("previewLeave", leave)
	ctx.Locals("previewCh", ch)
	ctx.Locals("previewRoom", roomID)
	return c.sseHandle(ctx)
}

func (c *Controller) sseHandle(ctx fiber.Ctx) error {
	if c.sseOnce == nil {
		c.sseOnce = sse.New(sse.Config{
			HeartbeatInterval: 25 * time.Second,
			Handler: func(ctx fiber.Ctx, stream *sse.Stream) error {
				leave := ctx.Locals("previewLeave").(func())
				ch := ctx.Locals("previewCh").(<-chan []byte)
				roomID := ctx.Locals("previewRoom").(int)
				defer leave()

				_ = stream.Comment("connected")
				origin := c.svc.LiveJoinClock(roomID)
				c.sendCatchup(stream, roomID, origin)

				for {
					select {
					case payload, ok := <-ch:
						if !ok {
							return nil
						}
						line := bytesTrimNewline(payload)
						if len(line) == 0 {
							continue
						}
						line = pvs.RebasePreviewTs(line, origin)
						if err := stream.Event(sse.Event{Data: line}); err != nil {
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

func (c *Controller) sendCatchup(stream *sse.Stream, roomID int, originSec float64) {
	stats, ok := c.rec.GetStats(roomID)
	if !ok || stats.OutputPath == "" {
		return
	}
	side, err := danmaku.ResolveSidecar(stats.OutputPath)
	if err != nil || !strings.HasSuffix(strings.ToLower(side), ".jsonl") {
		return
	}
	lines, err := c.svc.Catchup(side)
	if err != nil {
		log.Warnf("房间 %d 预览弹幕回放读取失败：%v", roomID, err)
		return
	}
	for _, line := range lines {
		if err := stream.Event(sse.Event{Data: pvs.RebasePreviewTs(line, originSec)}); err != nil {
			return
		}
	}
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

func bytesTrimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
