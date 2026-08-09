package danmaku

import (
	"encoding/json"

	"github.com/tidwall/gjson"
)

func parseDanmaku(raw []byte) (Danmaku, bool) {
	info := gjson.GetBytes(raw, "info")
	if !info.Exists() {
		return Danmaku{}, false
	}
	e := Danmaku{
		Text:         gjson.GetBytes(raw, "info.1").String(),
		UID:          gint(raw, "info.2.0", 0),
		Uname:        gjson.GetBytes(raw, "info.2.1").String(),
		Mode:         gint(raw, "info.0.1", 1),
		FontSize:     gint(raw, "info.0.2", 25),
		Color:        gint(raw, "info.0.3", 16777215),
		SendTime:     gint(raw, "info.0.4", 0),
		DmType:       gint(raw, "info.0.12", 0),
		GuardLevel:   gint(raw, "info.7", 0),
		UserLevel:    gint(raw, "info.2.16.0", 0),
		Admin:        gjson.GetBytes(raw, "info.2.2").Bool(),
		Urank:        gint(raw, "info.2.5", 0),
		MobileVerify: gjson.GetBytes(raw, "info.2.6").Bool(),
		MedalLevel:   gint(raw, "info.3.0", 0),
		MedalName:    gjson.GetBytes(raw, "info.3.1").String(),
		MedalUpName:  gjson.GetBytes(raw, "info.3.2").String(),
		MedalRoomID:  gint(raw, "info.3.3", 0),
		MedalColor:   gint(raw, "info.3.4", 0),
		MedalUpUID:   gint(raw, "info.3.12", 0),
	}
	// Extra JSON embedded at info.0.15.extra (string)
	extraRaw := gjson.GetBytes(raw, "info.0.15.extra").String()
	if extraRaw != "" {
		e.DmTypeExtra = gjson.Get(extraRaw, "dm_type").Int()
		e.Direction = gjson.Get(extraRaw, "direction").Int()
		e.EmoticonUnique = gjson.Get(extraRaw, "emoticon_unique").String()
	}
	emoRaw := gjson.GetBytes(raw, "info.0.13")
	if emoRaw.Exists() {
		if emoRaw.Type == gjson.String {
			e.EmoticonURL = gjson.Get(emoRaw.String(), "url").String()
			if e.EmoticonUnique == "" {
				e.EmoticonUnique = gjson.Get(emoRaw.String(), "emoticon_unique").String()
			}
		} else {
			e.EmoticonURL = emoRaw.Get("url").String()
			if e.EmoticonUnique == "" {
				e.EmoticonUnique = emoRaw.Get("emoticon_unique").String()
			}
		}
	}
	return e, true
}

func parseSuperChat(raw []byte) SuperChat {
	var sc SuperChat
	_ = json.Unmarshal([]byte(gjson.GetBytes(raw, "data").Raw), &sc)
	return sc
}

func parseGift(raw []byte) Gift {
	var g Gift
	_ = json.Unmarshal([]byte(gjson.GetBytes(raw, "data").Raw), &g)
	return g
}

func parseGuard(raw []byte) Guard {
	var g Guard
	_ = json.Unmarshal([]byte(gjson.GetBytes(raw, "data").Raw), &g)
	return g
}

func gint(raw []byte, path string, def int64) int64 {
	v := gjson.GetBytes(raw, path)
	if !v.Exists() {
		return def
	}
	return v.Int()
}
