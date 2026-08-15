package danmaku

import (
	"strconv"
	"time"

	"github.com/bytedance/sonic/encoder"
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

// Wire format keeps stable keys used by bilirec-web; extra fields are omitempty.
type jsonlDanmakuLine struct {
	Type           string  `json:"type"`
	TS             float64 `json:"ts"`
	User           string  `json:"user"`
	UID            int64   `json:"uid"`
	Text           string  `json:"text"`
	Mode           int64   `json:"mode"`
	FontSize       int64   `json:"font_size"`
	Color          int64   `json:"color"`
	SendTime       int64   `json:"send_time"`
	DmType         int64   `json:"dm_type,omitempty"`
	GuardLevel     int64   `json:"guard_level,omitempty"`
	UserLevel      int64   `json:"user_level,omitempty"`
	Admin          bool    `json:"admin,omitempty"`
	MedalLevel     int64   `json:"medal_level,omitempty"`
	MedalName      string  `json:"medal_name,omitempty"`
	Direction      int64   `json:"direction,omitempty"`
	EmoticonUnique string  `json:"emoticon_unique,omitempty"`
	EmoticonURL    string  `json:"emoticon_url,omitempty"`
}

// Fields required by bilirec-web parseJsonlDanmaku for Super Chat overlays.
type jsonlSuperChatLine struct {
	Type                  string  `json:"type"`
	TS                    float64 `json:"ts"`
	User                  string  `json:"user"`
	Message               string  `json:"message"`
	Price                 int64   `json:"price"`
	Time                  int64   `json:"time"`
	BackgroundColor       string  `json:"background_color,omitempty"`
	BackgroundBottomColor string  `json:"background_bottom_color,omitempty"`
	BackgroundPriceColor  string  `json:"background_price_color,omitempty"`
	MessageFontColor      string  `json:"message_font_color,omitempty"`
	BackgroundImage       string  `json:"background_image,omitempty"`
	NameColor             string  `json:"name_color,omitempty"`
	Face                  string  `json:"face,omitempty"`
}

// Fields required by bilirec-web for gift overlays.
type jsonlGiftLine struct {
	Type      string  `json:"type"`
	TS        float64 `json:"ts"`
	User      string  `json:"user"`
	GiftName  string  `json:"gift_name"`
	GiftCount int64   `json:"gift_count"`
	Face      string  `json:"face,omitempty"`
	NameColor string  `json:"name_color,omitempty"`
}

// Fields required by bilirec-web for guard overlays.
type jsonlGuardLine struct {
	Type  string  `json:"type"`
	TS    float64 `json:"ts"`
	User  string  `json:"user"`
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
		DmType: e.DmType, GuardLevel: e.GuardLevel, UserLevel: e.UserLevel, Admin: e.Admin,
		MedalLevel: e.MedalLevel, MedalName: e.MedalName,
		Direction: e.Direction, EmoticonUnique: e.EmoticonUnique, EmoticonURL: e.EmoticonURL,
	})
}

func (jsonlEncoder) AppendSuperChat(buf []byte, e SuperChat, ts string) []byte {
	return appendJSONLine(buf, jsonlSuperChatLine{
		Type: "super_chat", TS: parseTS(ts), User: e.Uname(),
		Message: e.Message, Price: e.Price, Time: e.Time,
		BackgroundColor: e.BackgroundColor, BackgroundBottomColor: e.BackgroundBottomColor,
		BackgroundPriceColor: e.BackgroundPriceColor, MessageFontColor: e.MessageFontColor,
		BackgroundImage: e.BackgroundImage, NameColor: e.UserInfo.NameColor, Face: e.UserInfo.Face,
	})
}

func (jsonlEncoder) AppendGift(buf []byte, e Gift, ts string) []byte {
	return appendJSONLine(buf, jsonlGiftLine{
		Type: "gift", TS: parseTS(ts), User: e.Uname,
		GiftName: e.GiftName, GiftCount: e.Num,
		Face: e.Face, NameColor: e.NameColor,
	})
}

func (jsonlEncoder) AppendGuard(buf []byte, e Guard, ts string) []byte {
	return appendJSONLine(buf, jsonlGuardLine{
		Type: "guard", TS: parseTS(ts), User: e.Username,
		Level: e.GuardLevel, Count: e.Num,
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
	if err := encoder.EncodeInto(&buf, v, 0); err != nil {
		return nil
	}
	return append(buf, '\n')
}
