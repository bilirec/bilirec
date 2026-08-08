package danmaku

import (
	"encoding/json"
	"strconv"
	"time"
)

type jsonlEncoder struct{}

func (jsonlEncoder) Ext() string { return ".jsonl" }

type jsonlMetaLine struct {
	Type      string `json:"type"`
	RoomID    int64  `json:"room_id"`
	ShortID   int64  `json:"short_id"`
	Name      string `json:"name"`
	Title     string `json:"title"`
	StartTime string `json:"start_time"`
}

type jsonlDanmakuLine struct {
	Type     string  `json:"type"`
	TS       float64 `json:"ts"`
	User     string  `json:"user"`
	UID      int64   `json:"uid"`
	Text     string  `json:"text"`
	Mode     int64   `json:"mode"`
	FontSize int64   `json:"font_size"`
	Color    int64   `json:"color"`
	SendTime int64   `json:"send_time"`
}

type jsonlSuperChatLine struct {
	Type    string  `json:"type"`
	TS      float64 `json:"ts"`
	User    string  `json:"user"`
	UID     int64   `json:"uid"`
	Price   int64   `json:"price"`
	Time    int64   `json:"time"`
	Message string  `json:"message"`
}

type jsonlGiftLine struct {
	Type      string  `json:"type"`
	TS        float64 `json:"ts"`
	User      string  `json:"user"`
	UID       int64   `json:"uid"`
	GiftName  string  `json:"gift_name"`
	GiftCount int64   `json:"gift_count"`
}

type jsonlGuardLine struct {
	Type  string  `json:"type"`
	TS    float64 `json:"ts"`
	User  string  `json:"user"`
	UID   int64   `json:"uid"`
	Level int64   `json:"level"`
	Count int64   `json:"count"`
}

func (jsonlEncoder) AppendHeader(buf []byte, meta RoomMeta, start time.Time) []byte {
	return appendJSONLine(buf, jsonlMetaLine{
		Type:      "meta",
		RoomID:    meta.RoomID,
		ShortID:   meta.ShortID,
		Name:      meta.Uname,
		Title:     meta.Title,
		StartTime: start.Format(time.RFC3339),
	})
}

func (jsonlEncoder) AppendFooter(buf []byte) []byte {
	return buf
}

func (jsonlEncoder) AppendDanmaku(buf []byte, e Danmaku, ts string) []byte {
	return appendJSONLine(buf, jsonlDanmakuLine{
		Type: "danmaku", TS: parseTS(ts), User: e.Uname, UID: e.UID, Text: e.Text,
		Mode: e.Mode, FontSize: e.FontSize, Color: e.Color, SendTime: e.SendTime,
	})
}

func (jsonlEncoder) AppendSuperChat(buf []byte, e SuperChat, ts string) []byte {
	return appendJSONLine(buf, jsonlSuperChatLine{
		Type: "super_chat", TS: parseTS(ts), User: e.Uname, UID: e.UID,
		Price: e.Price, Time: e.Time, Message: e.Message,
	})
}

func (jsonlEncoder) AppendGift(buf []byte, e Gift, ts string) []byte {
	return appendJSONLine(buf, jsonlGiftLine{
		Type: "gift", TS: parseTS(ts), User: e.Uname, UID: e.UID,
		GiftName: e.GiftName, GiftCount: e.GiftCount,
	})
}

func (jsonlEncoder) AppendGuard(buf []byte, e Guard, ts string) []byte {
	return appendJSONLine(buf, jsonlGuardLine{
		Type: "guard", TS: parseTS(ts), User: e.Uname, UID: e.UID,
		Level: e.Level, Count: e.Count,
	})
}

func parseTS(ts string) float64 {
	v, err := strconv.ParseFloat(ts, 64)
	if err != nil {
		return 0
	}
	return v
}

func appendJSONLine(buf []byte, v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	buf = append(buf, b...)
	buf = append(buf, '\n')
	return buf
}
