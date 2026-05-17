package room

import (
	"strconv"

	"github.com/eric2788/bilirec/internal/modules/bilibili"
	"github.com/eric2788/bilirec/internal/modules/rest"
	"github.com/eric2788/bilirec/internal/services/room"
	"github.com/eric2788/bilirec/internal/services/subscribe"
	"github.com/eric2788/bilirec/utils"
	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("controller", "room")

type Controller struct {
	roomSvc *room.Service
	subSvc  *subscribe.Service
}

func NewController(app *fiber.App, roomSvc *room.Service, subSvc *subscribe.Service) *Controller {
	rc := &Controller{
		roomSvc: roomSvc,
		subSvc:  subSvc,
	}
	room := app.Group("/room")
	room.Get("/:roomID/info", rc.getRoomInfo)
	room.Post("/infos", rc.getRoomInfos)
	room.Get("/:roomID/live", rc.getLiveStatus)
	room.Post("/lives", rc.getLiveStatuses)
	room.Get("/subscribe", rc.listSubscribeRooms)
	room.Get("/subscribe/:roomID", rc.isSubscribeRoom)
	room.Get("/:roomID/config", rc.getRoomConfig)

	room.Post("/:roomID", rest.AdminOnly, rc.subscribeRoom)
	room.Delete("/:roomID", rest.AdminOnly, rc.unsubscribeRoom)
	room.Put("/:roomID/config", rest.AdminOnly, rc.updateRoomConfig)
	return rc
}

// @Summary Get room information
// @Description Get detailed information about a Bilibili live room
// @Tags room
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param roomID path int true "Room ID"
// @Success 200 {object} bilibili.LiveRoomInfoDetail "Room information"
// @Failure 400 {string} string "Invalid room ID"
// @Failure 404 {string} string "Room not found"
// @Failure 500 {string} string "Internal server error"
// @Router /room/{roomID}/info [get]
func (r *Controller) getRoomInfo(ctx fiber.Ctx) error {
	roomId, err := strconv.Atoi(ctx.Params("roomID"))
	if err != nil {
		logger.Warnf("无法将 roomId 解析为整数：%v", err)
		return fiber.NewError(fiber.StatusBadRequest, "无效的房间 ID")
	}
	res, err := r.roomSvc.GetLiveRoomInfo(roomId)

	if err != nil {
		logger.Errorf("获取房间 %d 信息失败：%v", roomId, err)
		return utils.Ternary(
			bilibili.IsErrRoomNotFound(err),
			fiber.NewError(fiber.StatusNotFound, "房间不存在"),
			fiber.ErrInternalServerError,
		)
	}

	return ctx.JSON(res)
}

// @Summary Get multiple room informations
// @Description Get detailed information about multiple Bilibili live rooms. Provide roomIDs either via query parameter or JSON payload.
// @Tags room
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param roomIDs query string false "Comma-separated Room IDs (use this OR payload)"
// @Param payload body BatchRoomIDsRequest false "JSON payload, e.g. {\"roomIDs\":[123,456,789]} (use this OR roomIDs query)"
// @Success 200 {object} map[string]bilibili.LiveRoomInfoDetail "Map of Room ID to Room information"
// @Failure 400 {string} string "Invalid room IDs"
// @Failure 500 {string} string "Internal server error"
// @Router /room/infos [post]
func (r *Controller) getRoomInfos(ctx fiber.Ctx) error {
	roomIds, err := utils.ParseRoomIDs(ctx.Query("roomIDs", ""), ctx.Body())
	if err != nil {
		logger.Warnf("无法解析 roomIds：%v", err)
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	res, err := r.roomSvc.GetMultipleRoomInfos(roomIds...)
	if err != nil {
		logger.Errorf("批量获取房间 %v 信息失败：%v", roomIds, err)
		return fiber.ErrInternalServerError
	}
	return ctx.JSON(res)
}

// @Summary Check if stream is live
// @Description Check if a Bilibili live stream is currently live
// @Tags room
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param roomID path int true "Room ID"
// @Success 200 {object} LiveInfo "Live status"
// @Failure 400 {string} string "Invalid room ID"
// @Failure 404 {string} string "Room not found"
// @Failure 500 {string} string "Internal server error"
// @Router /room/{roomID}/live [get]
func (r *Controller) getLiveStatus(ctx fiber.Ctx) error {
	roomId, err := strconv.Atoi(ctx.Params("roomID"))
	if err != nil {
		logger.Warnf("无法将 roomId 解析为整数：%v", err)
		return fiber.NewError(fiber.StatusBadRequest, "无效的房间 ID")
	}
	isLive, err := r.roomSvc.IsRoomLive(roomId)
	if err != nil {
		logger.Errorf("检查房间 %d 直播状态失败：%v", roomId, err)
		return utils.Ternary(
			bilibili.IsErrRoomNotFound(err),
			fiber.NewError(fiber.StatusNotFound, "房间不存在"),
			fiber.ErrInternalServerError,
		)
	}
	return ctx.JSON(LiveInfo{
		RoomId: roomId,
		IsLive: isLive,
	})
}

// @Summary Check batch live status
// @Description Check if multiple Bilibili live streams are currently live. Provide roomIDs either via query parameter or JSON payload.
// @Tags room
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param roomIDs query string false "Comma-separated Room IDs (use this OR payload)"
// @Param payload body BatchRoomIDsRequest false "JSON payload, e.g. {\"roomIDs\":[123,456,789]} (use this OR roomIDs query)"
// @Success 200 {object} map[string]bool "Live statuses map"
// @Failure 400 {string} string "Invalid room IDs"
// @Router /room/lives [post]
func (r *Controller) getLiveStatuses(ctx fiber.Ctx) error {
	roomIds, err := utils.ParseRoomIDs(ctx.Query("roomIDs", ""), ctx.Body())
	if err != nil {
		logger.Warnf("无法解析 roomIds：%v", err)
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	return ctx.JSON(r.roomSvc.GetBatchLiveStatus(roomIds))
}

// @Summary Subscribe to room
// @Description Subscribe to a Bilibili live room for updates
// @Tags room
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param roomID path int true "Room ID"
// @Success 200 {string} string "Subscription successful"
// @Failure 400 {string} string "Invalid room ID"
// @Failure 403 {string} string "Not Admin"
// @Failure 404 {string} string "Room not found"
// @Failure 409 {string} string "Already subscribed"
// @Failure 500 {string} string "Internal server error"
// @Router /room/{roomID} [post]
func (r *Controller) subscribeRoom(ctx fiber.Ctx) error {
	roomId, err := strconv.Atoi(ctx.Params("roomID"))
	if err != nil {
		logger.Warnf("无法将 roomId 解析为整数：%v", err)
		return fiber.NewError(fiber.StatusBadRequest, "无效的房间 ID")
	}
	err = r.subSvc.Subscribe(roomId)
	if err != nil {
		logger.Errorf("订阅房间 %d 失败：%v", roomId, err)
		switch {
		case subscribe.ErrRoomAlreadySubscribed == err:
			return fiber.NewError(fiber.StatusConflict, "已订阅该房间")
		case bilibili.IsErrRoomNotFound(err):
			return fiber.NewError(fiber.StatusNotFound, "房间不存在")
		default:
			return fiber.ErrInternalServerError
		}
	}
	return ctx.SendStatus(fiber.StatusOK)
}

// @Summary Unsubscribe from room
// @Description Unsubscribe from a Bilibili live room
// @Tags room
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param roomID path int true "Room ID"
// @Success 200 {string} string "Unsubscription successful"
// @Failure 400 {string} string "Invalid room ID"
// @Failure 403 {string} string "Not Admin"
// @Failure 404 {string} string "Not subscribed"
// @Failure 500 {string} string "Internal server error"
// @Router /room/{roomID} [delete]
func (r *Controller) unsubscribeRoom(ctx fiber.Ctx) error {
	roomId, err := strconv.Atoi(ctx.Params("roomID"))
	if err != nil {
		logger.Warnf("无法将 roomId 解析为整数：%v", err)
		return fiber.NewError(fiber.StatusBadRequest, "无效的房间 ID")
	}
	err = r.subSvc.Unsubscribe(roomId)
	if err != nil {
		logger.Errorf("取消订阅房间 %d 失败：%v", roomId, err)
		return utils.Ternary(
			subscribe.ErrRoomNotSubscribed == err,
			fiber.NewError(fiber.StatusNotFound, "未订阅该房间"),
			fiber.ErrInternalServerError,
		)
	}
	return ctx.SendStatus(fiber.StatusOK)
}

// @Summary Check if room is subscribed
// @Description Check if a Bilibili live room is subscribed
// @Tags room
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param roomID path int true "Room ID"
// @Success 200 {object} SubscribeStatus "Subscription status"
// @Failure 400 {string} string "Invalid room ID"
// @Failure 500 {string} string "Internal server error"
// @Router /room/subscribe/{roomID} [get]
func (r *Controller) isSubscribeRoom(ctx fiber.Ctx) error {
	roomId, err := strconv.Atoi(ctx.Params("roomID"))
	if err != nil {
		logger.Warnf("无法将 roomId 解析为整数：%v", err)
		return fiber.NewError(fiber.StatusBadRequest, "无效的房间 ID")
	}
	isSubscribed, err := r.subSvc.IsSubscribed(roomId)
	if err != nil {
		logger.Errorf("检查房间 %d 订阅状态失败：%v", roomId, err)
		return fiber.ErrInternalServerError
	}
	return ctx.JSON(SubscribeStatus{
		RoomId:       roomId,
		IsSubscribed: isSubscribed,
	})
}

// @Summary List subscribed rooms
// @Description List all subscribed Bilibili live rooms
// @Tags room
// @Security BearerAuth
// @Accept json
// @Produce json
// @Success 200 {object} SubscribeList "List of subscribed Room IDs"
// @Failure 500 {string} string "Internal server error"
// @Router /room/subscribe [get]
func (r *Controller) listSubscribeRooms(ctx fiber.Ctx) error {
	roomIds, err := r.subSvc.ListSubscribedRooms()
	if err != nil {
		logger.Errorf("列出已订阅房间失败：%v", err)
		return fiber.ErrInternalServerError
	}
	return ctx.JSON(SubscribeList{
		RoomIds: roomIds,
	})
}

// @Summary Get room subscription config
// @Description Get subscription config for a room
// @Tags room
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param roomID path int true "Room ID"
// @Success 200 {object} RoomConfigResponse "Room config"
// @Failure 400 {string} string "Invalid room ID"
// @Failure 404 {string} string "Not subscribed"
// @Failure 500 {string} string "Internal server error"
// @Router /room/{roomID}/config [get]
func (r *Controller) getRoomConfig(ctx fiber.Ctx) error {
	roomId, err := strconv.Atoi(ctx.Params("roomID"))
	if err != nil {
		logger.Warnf("无法将 roomId 解析为整数：%v", err)
		return fiber.NewError(fiber.StatusBadRequest, "无效的房间 ID")
	}

	cfg, err := r.subSvc.GetConfig(roomId)
	if err != nil {
		logger.Errorf("获取房间 %d 配置失败：%v", roomId, err)
		if err == subscribe.ErrRoomNotSubscribed {
			return fiber.NewError(fiber.StatusNotFound, "未订阅该房间")
		}
		return fiber.ErrInternalServerError
	}

	return ctx.JSON(RoomConfigResponse{
		RoomId:                roomId,
		AutoRecord:            cfg.AutoRecord,
		Notify:                cfg.Notify,
		RecordDurationMinutes: cfg.RecordDurationMinutes,
	})
}

// @Summary Update room subscription config
// @Description Update subscription config for a room
// @Tags room
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param roomID path int true "Room ID"
// @Param request body UpdateRoomConfigRequest true "Room config"
// @Success 200 {object} RoomConfigResponse "Updated room config"
// @Failure 400 {string} string "Invalid request"
// @Failure 403 {string} string "Not Admin"
// @Failure 404 {string} string "Not subscribed"
// @Failure 500 {string} string "Internal server error"
// @Router /room/{roomID}/config [put]
func (r *Controller) updateRoomConfig(ctx fiber.Ctx) error {
	roomId, err := strconv.Atoi(ctx.Params("roomID"))
	if err != nil {
		logger.Warnf("无法将 roomId 解析为整数：%v", err)
		return fiber.NewError(fiber.StatusBadRequest, "无效的房间 ID")
	}

	var req UpdateRoomConfigRequest
	if err := ctx.Bind().Body(&req); err != nil {
		logger.Warnf("无法解析更新房间配置的请求体：%v", err)
		return fiber.NewError(fiber.StatusBadRequest, "无效的请求数据")
	}

	if err := r.subSvc.UpdateConfig(roomId, &subscribe.RoomConfig{AutoRecord: req.AutoRecord, Notify: req.Notify, RecordDurationMinutes: req.RecordDurationMinutes}); err != nil {
		logger.Errorf("更新房间 %d 配置失败：%v", roomId, err)
		if err == subscribe.ErrRoomNotSubscribed {
			return fiber.NewError(fiber.StatusNotFound, "未订阅该房间")
		}
		return fiber.ErrInternalServerError
	}

	return ctx.JSON(RoomConfigResponse{
		RoomId:                roomId,
		AutoRecord:            req.AutoRecord,
		Notify:                req.Notify,
		RecordDurationMinutes: req.RecordDurationMinutes,
	})
}
