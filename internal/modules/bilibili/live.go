package bilibili

import (
	"encoding/json"
	"net/url"
	"strconv"

	bili "github.com/CuteReimu/bilibili/v2"
	"github.com/pkg/errors"
)

type (
	LiveRoomInfoResponse struct {
		Code    int                    `json:"code"`
		Message string                 `json:"message"`
		TTL     int                    `json:"ttl"`
		Data    *LiveRoomInfoDataField `json:"data"`
	}

	LiveRoomInfoDataField struct {
		ByRoomIDs map[string]*LiveRoomInfoDetail `json:"by_room_ids"`
	}

	LiveRoomInfoDetail struct {
		RoomID       int64  `json:"room_id"`
		UID          int64  `json:"uid"`
		AreaID       int64  `json:"area_id"`
		LiveStatus   int    `json:"live_status"`
		LiveURL      string `json:"live_url"`
		ParentAreaID int64  `json:"parent_area_id"`

		Title          string `json:"title"`
		ParentAreaName string `json:"parent_area_name"`
		AreaName       string `json:"area_name"`
		LiveTime       string `json:"live_time"`
		Description    string `json:"description"`
		Tags           string `json:"tags"`

		Attention  int64  `json:"attention"`
		Online     int64  `json:"online"`
		ShortID    int64  `json:"short_id"`
		Uname      string `json:"uname"`
		Cover      string `json:"cover"`
		Background string `json:"background"`

		JoinSlide    int64  `json:"join_slide"`
		LiveID       int64  `json:"live_id"`
		LiveIDStr    string `json:"live_id_str"`
		LockStatus   int    `json:"lock_status"`
		HiddenStatus int    `json:"hidden_status"`
		IsEncrypted  bool   `json:"is_encrypted"`
	}
)

const liveRoomInfov1 = "https://api.live.bilibili.com/xlive/web-room/v1/index/getRoomBaseInfo"

func (c *Client) IsStreamLiving(roomID int) (bool, error) {
	info, err := c.GetLiveRoomInfo(roomID)
	if err != nil {
		return false, err
	}
	return info.LiveStatus == 1, nil
}

func (c *Client) GetLiveRoomInfos(roomIDs ...int) (map[string]*LiveRoomInfoDetail, error) {
	req := c.liveClient.R()
	queries := url.Values{}
	queries.Set("req_biz", "web_room_componet") // hard coded value
	for _, id := range roomIDs {
		queries.Add("room_ids", strconv.Itoa(id))
	}
	req.SetQueryParamsFromValues(queries)
	res, err := req.Get(liveRoomInfov1)
	if err != nil {
		return nil, err
	}
	var resp LiveRoomInfoResponse
	if err := json.Unmarshal(res.Body(), &resp); err != nil {
		return nil, err
	} else if resp.Code != 0 {
		return nil, errors.Errorf("failed to get live room infos: %s (code: %d)", resp.Message, resp.Code)
	} else if resp.Data == nil {
		return nil, errors.New("直播间信息响应中没有数据")
	} else if len(resp.Data.ByRoomIDs) == 0 {
		return nil, ErrRoomNotFound
	}
	return resp.Data.ByRoomIDs, nil
}

func (c *Client) GetLiveRoomInfo(roomID int) (*LiveRoomInfoDetail, error) {
	infos, err := c.GetLiveRoomInfos(roomID)
	if err != nil {
		return nil, err
	}
	info, ok := infos[strconv.Itoa(roomID)]
	if !ok {
		return nil, ErrRoomNotFound
	}
	return info, nil
}

func IsErrRoomNotFound(err error) bool {
	if err == ErrRoomNotFound {
		return true
	}
	cause := errors.Cause(err)
	if biliErr, ok := cause.(bili.Error); ok && biliErr.Code == 1 {
		return true
	}
	return false
}
