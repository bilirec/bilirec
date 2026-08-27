package bilibili

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	danmuInfoAPI          = "https://api.live.bilibili.com/xlive/web-room/v1/index/getDanmuInfo"
	defaultDanmakuHost    = "broadcastlv.chat.bilibili.com"
	defaultDanmakuWssPort = 443
)

// DanmuInfoHost is one candidate danmaku websocket server.
type DanmuInfoHost struct {
	Host    string `json:"host"`
	WssPort int    `json:"wss_port"`
}

// DanmuInfo holds the token and server candidates used to enter the danmaku
// websocket of a live room.
type DanmuInfo struct {
	Token    string
	HostList []DanmuInfoHost
}

type danmuInfoResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Token    string          `json:"token"`
		HostList []DanmuInfoHost `json:"host_list"`
	} `json:"data"`
}

// GetDanmuInfo fetches the danmaku websocket token and host list for a room.
// The request is WBI-signed like other live web APIs.
//
// On any failure (including risk-control error codes such as 352) it degrades
// to the default broadcast host with an empty token, which bilibili accepts
// for anonymous-level danmaku access; callers therefore always get a usable
// DanmuInfo and should rely on their own retry/backoff for connection errors.
func (c *Client) GetDanmuInfo(ctx context.Context, roomID int) (*DanmuInfo, error) {
	c.danmakuOnce.Do(func() {
		ensureBuvid3Cookie(c.liveClient)
	})

	client := c.liveClient.R().SetContext(ctx)
	client.SetQueryParams(map[string]string{
		"id":   fmt.Sprint(roomID),
		"type": "0",
	})
	newQueryParam, err := c.wbi.SignQuery(client.QueryParam, time.Now())
	if err != nil {
		return nil, fmt.Errorf("无法签名 wbi：%v", err)
	}
	client.QueryParam = newQueryParam

	resp, err := client.Get(danmuInfoAPI)
	if err != nil {
		return fallbackDanmuInfo(fmt.Errorf("请求 getDanmuInfo 失败：%v", err))
	} else if resp.StatusCode() != http.StatusOK {
		return fallbackDanmuInfo(fmt.Errorf("getDanmuInfo 状态码：%d", resp.StatusCode()))
	}

	var dir danmuInfoResponse
	if err := json.Unmarshal(resp.Body(), &dir); err != nil {
		return fallbackDanmuInfo(fmt.Errorf("解析 getDanmuInfo 响应失败：%v", err))
	} else if dir.Code != 0 {
		return fallbackDanmuInfo(fmt.Errorf("getDanmuInfo 错误码：%d（%s）", dir.Code, dir.Message))
	}

	info := &DanmuInfo{Token: dir.Data.Token, HostList: dir.Data.HostList}
	if len(info.HostList) == 0 {
		info.HostList = []DanmuInfoHost{{Host: defaultDanmakuHost, WssPort: defaultDanmakuWssPort}}
	}
	return info, nil
}

func fallbackDanmuInfo(cause error) (*DanmuInfo, error) {
	log.Warnf("getDanmuInfo 失败，降级使用默认弹幕服务器：%v", cause)
	return &DanmuInfo{
		Token:    "",
		HostList: []DanmuInfoHost{{Host: defaultDanmakuHost, WssPort: defaultDanmakuWssPort}},
	}, nil
}

// DanmakuIdentity returns the uid and buvid3 used in the danmaku websocket
// enter packet. Both may be zero/empty in anonymous mode.
func (c *Client) DanmakuIdentity() (uid int, buvid string) {
	if acc := c.GetSession().Account; acc != nil {
		uid = acc.Mid
	}
	for _, cookie := range c.liveClient.Cookies {
		if cookie.Name == "buvid3" && cookie.Value != "" {
			buvid = cookie.Value
			break
		}
	}
	if buvid == "" {
		for _, cookie := range c.GetCookies() {
			if cookie.Name == "buvid3" && cookie.Value != "" {
				buvid = cookie.Value
				break
			}
		}
	}
	return
}
