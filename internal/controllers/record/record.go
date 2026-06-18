package record

import (
	"strconv"
	"strings"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/rest"
	"github.com/bilirec/bilirec/internal/services/recorder"
	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"

	"github.com/bilirec/bilirec/utils"
)

var logger = logrus.WithField("controller", "record")

type Controller struct {
	service *recorder.Service
}

func NewController(app *fiber.App, service *recorder.Service) *Controller {
	rc := &Controller{service: service}
	record := app.Group("/record")
	record.Get("/list", rc.listRecordings)
	record.Post("/statuses", rc.getRecordingStatuses)
	record.Post("/stats", rc.getRecordingStats)
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
// @Param duration_minutes query int false "Recording duration in minutes. 0 = system default, -1 = unlimited, >0 = stop after N minutes, omit = system default (MAX_RECORDING_HOURS)"
// @Param stream_profile query string false "Preferred stream profile: http-flv | hls-ts | hls-fmp4"
// @Param qn query int false "Stream quality code: 80,150,250,400,10000,20000,30000"
// @Param only_audio query bool false "Whether to request only audio stream"
// @Success 200 "Recording started successfully"
// @Failure 400 {string} string "Invalid room ID"
// @Failure 403 {string} string "Forbidden"
// @Failure 500 {string} string "Internal server error"
// @Router /record/{roomID}/start [post]
func (r *Controller) startRecording(ctx fiber.Ctx) error {
	roomId, err := strconv.Atoi(ctx.Params("roomID"))
	if err != nil {
		logger.Warnf("无法将 roomId 解析为整数：%v", err)
		return fiber.NewError(fiber.StatusBadRequest, "无效的房间 ID")
	}

	// duration_minutes query param: 0 (sentinel) = not provided → use system default
	const notProvided = 0
	durationMinutes := fiber.Query(ctx, "duration_minutes", notProvided)

	var startArgs []recorder.RecordStartOption
	switch {
	case durationMinutes == -1:
		startArgs = []recorder.RecordStartOption{recorder.WithDuration(0)} // unlimited
	case durationMinutes > 0:
		startArgs = []recorder.RecordStartOption{recorder.WithDuration(time.Duration(durationMinutes) * time.Minute)}
	}
	// durationMinutes == 0 (not provided): pass no args → system default

	streamOptions := []bilibili.GetStreamURLsOption{}
	streamProfileRaw := strings.TrimSpace(fiber.Query(ctx, "stream_profile", ""))
	if streamProfileRaw != "" {
		streamOptions = append(streamOptions, bilibili.WithProfiles(bilibili.StreamProfile(streamProfileRaw)))
	}

	qnRaw := strings.TrimSpace(fiber.Query(ctx, "qn", ""))
	if qnRaw != "" {
		qn, err := strconv.Atoi(qnRaw)
		if err != nil {
			logger.Warnf("无法将 qn 解析为整数：%v", err)
			return fiber.NewError(fiber.StatusBadRequest, "无效的 qn 参数")
		}
		streamOptions = append(streamOptions, bilibili.WithQn(bilibili.Quality(qn)))
	}

	onlyAudioRaw := strings.TrimSpace(strings.ToLower(fiber.Query(ctx, "only_audio", "false")))
	if onlyAudio, _ := strconv.ParseBool(onlyAudioRaw); onlyAudio {
		streamOptions = append(streamOptions, bilibili.WithOnlyAudio(true))
	}

	startArgs = append(startArgs, recorder.WithStreamOptions(streamOptions...))
	err = r.service.Start(roomId, startArgs...)
	if err != nil {
		logger.Errorf("为房间 %d 开始录制失败：%v", roomId, err)
		switch err {
		case bilibili.ErrRoomNotFound:
			return fiber.NewError(fiber.StatusNotFound, "房间不存在")
		case recorder.ErrRoomBanned:
			return fiber.NewError(fiber.StatusBadRequest, "房间已被封禁")
		case recorder.ErrRoomEncrypted:
			return fiber.NewError(fiber.StatusBadRequest, "房间已被上锁")
		case recorder.ErrEmptyStreamURLs:
			return fiber.NewError(fiber.StatusBadRequest, "无可用的视频流 URL")
		case recorder.ErrStreamNotLive:
			return fiber.NewError(fiber.StatusBadRequest, "房间并非直播状态")
		case recorder.ErrRecordingStarted:
			return fiber.NewError(fiber.StatusBadRequest, "该房间已在录制中")
		case recorder.ErrRecordRecovering:
			return fiber.NewError(fiber.StatusBadRequest, "该房间正在恢复流")
		case recorder.ErrRecordingPending:
			return fiber.NewError(fiber.StatusConflict, "该房间录制正在启动中")
		case recorder.ErrMaxConcurrentRecordingsReached:
			return fiber.NewError(fiber.StatusTooManyRequests, "已达到最大同时录制数")
		case recorder.ErrInsufficientDiskSpace:
			return fiber.NewError(fiber.StatusInsufficientStorage, "磁盘空间低于设定值")
		case bilibili.ErrInvalidStreamProfile:
			return fiber.NewError(fiber.StatusBadRequest, "无效的流格式，仅支持 http-flv / hls-ts / hls-fmp4")
		case bilibili.ErrInvalidStreamCodec:
			return fiber.NewError(fiber.StatusBadRequest, "无效的编码格式，仅支持 AVC / HEVC / 其他")
		case bilibili.ErrInvalidStreamQuality:
			return fiber.NewError(fiber.StatusBadRequest, "无效的清晰度，仅支持 流畅 / 高清 / 超清 / 蓝光 / 原画 / 4K / 杜比")
		case recorder.ErrStreamURLsUnreachable, recorder.ErrEmptyStreamURLs:
			return fiber.NewError(fiber.StatusGone, "无法连接到视频流 URL")
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
		logger.Warnf("无法将 roomId 解析为整数：%v", err)
		return fiber.NewError(fiber.StatusBadRequest, "无效的房间 ID")
	}
	stopped := r.service.Stop(roomId)
	return ctx.JSON(StopResult{
		RoomId:  roomId,
		Success: stopped,
	})
}

// @Summary Get batch recording statuses
// @Description Get the current recording statuses for multiple rooms. Provide roomIDs either via query parameter or JSON payload.
// @Tags record
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param roomIDs query string false "Comma-separated Room IDs (use this OR payload)"
// @Param payload body BatchRoomIDsRequest false "JSON payload, e.g. {\"roomIDs\":[123,456,789]} (use this OR roomIDs query)"
// @Success 200 {object} map[string]string "Recording statuses map"
// @Failure 400 {string} string "Invalid room IDs"
// @Router /record/statuses [post]
func (r *Controller) getRecordingStatuses(ctx fiber.Ctx) error {
	roomIds, err := utils.ParseRoomIDs(ctx.Query("roomIDs", ""), ctx.Body())
	if err != nil {
		logger.Warnf("无法解析 roomIds：%v", err)
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	statusMap := r.service.GetStatuses(roomIds)
	result := make(map[string]string, len(statusMap))
	for roomIdStr, status := range statusMap {
		result[roomIdStr] = string(status)
	}
	return ctx.JSON(result)
}

// @Summary Get batch recording stats
// @Description Get detailed recording statistics for multiple rooms. Provide roomIDs either via query parameter or JSON payload.
// @Tags record
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param roomIDs query string false "Comma-separated Room IDs (use this OR payload)"
// @Param payload body BatchRoomIDsRequest false "JSON payload, e.g. {\"roomIDs\":[123,456,789]} (use this OR roomIDs query)"
// @Success 200 {object} map[string]recorder.Stats "Recording stats map"
// @Failure 400 {string} string "Invalid room IDs"
// @Router /record/stats [post]
func (r *Controller) getRecordingStats(ctx fiber.Ctx) error {
	roomIds, err := utils.ParseRoomIDs(ctx.Query("roomIDs", ""), ctx.Body())
	if err != nil {
		logger.Warnf("无法解析 roomIds：%v", err)
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	return ctx.JSON(r.service.GetBatchStats(roomIds))
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
