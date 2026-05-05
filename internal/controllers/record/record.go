package record

import (
	"strconv"
	"time"

	"github.com/eric2788/bilirec/internal/modules/bilibili"
	"github.com/eric2788/bilirec/internal/modules/rest"
	"github.com/eric2788/bilirec/internal/services/recorder"
	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("controller", "record")

type Controller struct {
	service *recorder.Service
}

func NewController(app *fiber.App, service *recorder.Service) *Controller {
	rc := &Controller{service: service}
	record := app.Group("/record")
	record.Get("/list", rc.listRecordings)
	record.Get("/:roomID/status", rc.getRecordingStatus)
	record.Get("/:roomID/stats", rc.getRecordingStats)
	record.Post("/:roomID/start", rest.AdminOnly, rc.startRecording)
	record.Post("/:roomID/stop", rest.AdminOnly, rc.stopRecording)
	return rc
}

// @Summary Start recording a live stream
// @Description Start recording a Bilibili live stream for the specified room
// @Tags record
// @Security BearerAuth
// @Produce json
// @Param roomID path int true "Room ID"
// @Param duration_minutes query int false "Recording duration in minutes. 0 = unlimited, >0 = stop after N minutes, omit = system default (MAX_RECORDING_HOURS)"
// @Success 200 "Recording started successfully"
// @Failure 400 {string} string "Invalid room ID"
// @Failure 403 {string} string "Forbidden"
// @Failure 500 {string} string "Internal server error"
// @Router /record/{roomID}/start [post]
func (r *Controller) startRecording(ctx fiber.Ctx) error {
	roomId, err := strconv.Atoi(ctx.Params("roomID"))
	if err != nil {
		logger.Warnf("cannot parse roomId to int: %v", err)
		return fiber.NewError(fiber.StatusBadRequest, "無效的房間 ID")
	}

	// duration_minutes query param: -1 (sentinel) = not provided → use system default
	const notProvided = -1
	durationMinutes := fiber.Query(ctx, "duration_minutes", notProvided)

	var startArgs []time.Duration
	switch {
	case durationMinutes == 0:
		startArgs = []time.Duration{0} // unlimited
	case durationMinutes > 0:
		startArgs = []time.Duration{time.Duration(durationMinutes) * time.Minute}
	}
	// durationMinutes == -1 (not provided): pass no args → system default

	err = r.service.Start(roomId, startArgs...)
	if err != nil {
		logger.Errorf("error starting recording for room %d: %v", roomId, err)
		switch err {
		case bilibili.ErrRoomNotFound:
			return fiber.NewError(fiber.StatusNotFound, "房間不存在")
		case recorder.ErrRoomBanned:
			return fiber.NewError(fiber.StatusBadRequest, "房間已被封禁")
		case recorder.ErrRoomEncrypted:
			return fiber.NewError(fiber.StatusBadRequest, "房間已被上鎖")
		case recorder.ErrEmptyStreamURLs:
			return fiber.NewError(fiber.StatusBadRequest, "無可用的視頻流 URL")
		case recorder.ErrStreamNotLive:
			return fiber.NewError(fiber.StatusBadRequest, "房間並非直播狀態")
		case recorder.ErrRecordingStarted:
			return fiber.NewError(fiber.StatusBadRequest, "此房間已經正在錄製中")
		case recorder.ErrMaxConcurrentRecordingsReached:
			return fiber.NewError(fiber.StatusTooManyRequests, "已達到最大同時錄製數")
		case recorder.ErrInsufficientDiskSpace:
			return fiber.NewError(fiber.StatusInsufficientStorage, "磁碟空間低於設定值")
		default:
			return fiber.ErrInternalServerError
		}
	}
	return ctx.SendStatus(fiber.StatusOK)
}

// @Summary Stop recording a live stream
// @Description Stop recording a Bilibili live stream for the specified room
// @Tags record
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param roomID path int true "Room ID"
// @Success 200 {object} StopResult "Recording stopped"
// @Failure 400 {string} string "Invalid room ID"
// @Failure 403 {string} string "Forbidden"
// @Router /record/{roomID}/stop [post]
func (r *Controller) stopRecording(ctx fiber.Ctx) error {
	roomId, err := strconv.Atoi(ctx.Params("roomID"))
	if err != nil {
		logger.Warnf("cannot parse roomId to int: %v", err)
		return fiber.NewError(fiber.StatusBadRequest, "無效的房間 ID")
	}
	stopped := r.service.Stop(roomId)
	return ctx.JSON(StopResult{
		RoomId:  roomId,
		Success: stopped,
	})
}

// @Summary Get recording status
// @Description Get the current recording status for a specific room
// @Tags record
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param roomID path int true "Room ID"
// @Success 200 {object} Status "Recording status"
// @Failure 400 {string} string "Invalid room ID"
// @Router /record/{roomID}/status [get]
func (r *Controller) getRecordingStatus(ctx fiber.Ctx) error {
	roomId, err := strconv.Atoi(ctx.Params("roomID"))
	if err != nil {
		logger.Warnf("cannot parse roomId to int: %v", err)
		return fiber.NewError(fiber.StatusBadRequest, "無效的房間 ID")
	}
	status := r.service.GetStatus(roomId)
	return ctx.JSON(Status{
		RoomId: roomId,
		Status: status,
	})
}

// @Summary Get recording statistics
// @Description Get recording statistics for a specific room
// @Tags record
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param roomID path int true "Room ID"
// @Success 200 {object} recorder.Stats "Recording statistics"
// @Failure 400 {string} string "Invalid room ID"
// @Failure 404 {string} string "Recording not found"
// @Router /record/{roomID}/stats [get]
func (r *Controller) getRecordingStats(ctx fiber.Ctx) error {
	roomId, err := strconv.Atoi(ctx.Params("roomID"))
	if err != nil {
		logger.Warnf("cannot parse roomId to int: %v", err)
		return fiber.NewError(fiber.StatusBadRequest, "無效的房間 ID")
	}
	stats, ok := r.service.GetStats(roomId)
	if !ok {
		return fiber.ErrNotFound
	}
	return ctx.JSON(stats)
}

// @Summary List all recordings
// @Description Get a list of all room IDs that are currently being recorded
// @Tags record
// @Security BearerAuth
// @Accept json
// @Produce json
// @Success 200 {array} int64 "List of room IDs"
// @Router /record/list [get]
func (r *Controller) listRecordings(ctx fiber.Ctx) error {
	roomIds := r.service.ListRecording()
	return ctx.JSON(roomIds)
}
