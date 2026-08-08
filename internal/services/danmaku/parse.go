package danmaku

import (
	"github.com/tidwall/gjson"
)

// Danmaku is a parsed chat message.
type Danmaku struct {
	Text     string
	UID      int64
	Uname    string
	Mode     int64
	FontSize int64
	Color    int64
	SendTime int64
}

// SuperChat is a parsed paid highlight message.
type SuperChat struct {
	Uname   string
	UID     int64
	Price   int64
	Time    int64
	Message string
}

// Gift is a parsed gift event.
type Gift struct {
	Uname     string
	UID       int64
	GiftName  string
	GiftCount int64
}

// Guard is a parsed membership purchase event.
type Guard struct {
	Uname string
	UID   int64
	Level int64
	Count int64
}

func parseDanmaku(raw []byte) (Danmaku, bool) {
	info := gjson.GetBytes(raw, "info")
	if !info.Exists() {
		return Danmaku{}, false
	}
	return Danmaku{
		Text:     gjson.GetBytes(raw, "info.1").String(),
		UID:      gint(raw, "info.2.0", 0),
		Uname:    gjson.GetBytes(raw, "info.2.1").String(),
		Mode:     gint(raw, "info.0.1", 1),
		FontSize: gint(raw, "info.0.2", 25),
		Color:    gint(raw, "info.0.3", 16777215),
		SendTime: gint(raw, "info.0.4", 0),
	}, true
}

func parseSuperChat(raw []byte) SuperChat {
	return SuperChat{
		Uname:   gjson.GetBytes(raw, "data.user_info.uname").String(),
		UID:     gint(raw, "data.uid", 0),
		Price:   gint(raw, "data.price", 0),
		Time:    gint(raw, "data.time", 0),
		Message: gjson.GetBytes(raw, "data.message").String(),
	}
}

func parseGift(raw []byte) Gift {
	return Gift{
		Uname:     gjson.GetBytes(raw, "data.uname").String(),
		UID:       gint(raw, "data.uid", 0),
		GiftName:  gjson.GetBytes(raw, "data.giftName").String(),
		GiftCount: gint(raw, "data.num", 0),
	}
}

func parseGuard(raw []byte) Guard {
	return Guard{
		Uname: gjson.GetBytes(raw, "data.username").String(),
		UID:   gint(raw, "data.uid", 0),
		Level: gint(raw, "data.guard_level", 0),
		Count: gint(raw, "data.num", 0),
	}
}

func gint(raw []byte, path string, def int64) int64 {
	v := gjson.GetBytes(raw, path)
	if !v.Exists() {
		return def
	}
	return v.Int()
}
