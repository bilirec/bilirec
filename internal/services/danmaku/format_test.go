package danmaku

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestXMLEncoderElements(t *testing.T) {
	enc := xmlEncoder{}

	dm := string(enc.AppendDanmaku(nil, Danmaku{
		Text: `测试<弹幕>&"'`, UID: 123456, Uname: "测试用户",
		Mode: 1, FontSize: 25, Color: 16777215, SendTime: 1754668800000,
	}, "1.500"))
	wantDM := `<d p="1.500,1,25,16777215,1754668800000,0,123456,0" user="测试用户">测试&lt;弹幕&gt;&amp;&quot;&#39;</d>` + "\n"
	if dm != wantDM {
		t.Errorf("danmaku:\ngot  %q\nwant %q", dm, wantDM)
	}

	sc := SuperChat{UID: 123, Price: 30, Time: 60, Message: "醒目<留言>"}
	sc.UserInfo.Uname = "sc用户"
	gotSC := string(enc.AppendSuperChat(nil, sc, "2.250"))
	wantSC := `<sc ts="2.250" user="sc用户" uid="123" price="30" time="60" background_color="" background_bottom_color="" background_price_color="" message_font_color="" background_image="" name_color="">醒目&lt;留言&gt;</sc>` + "\n"
	if gotSC != wantSC {
		t.Errorf("sc:\ngot  %q\nwant %q", gotSC, wantSC)
	}

	gift := string(enc.AppendGift(nil, Gift{
		Uname: "礼物用户", UID: 456, GiftName: "小花花", Num: 2,
	}, "3.000"))
	wantGift := `<gift ts="3.000" user="礼物用户" uid="456" giftname="小花花" giftcount="2"/>` + "\n"
	if gift != wantGift {
		t.Errorf("gift:\ngot  %q\nwant %q", gift, wantGift)
	}

	guard := string(enc.AppendGuard(nil, Guard{
		Username: "舰长用户", UID: 789, GuardLevel: 3, Num: 1,
	}, "4.000"))
	wantGuard := `<guard ts="4.000" user="舰长用户" uid="789" level="3" count="1"/>` + "\n"
	if guard != wantGuard {
		t.Errorf("guard:\ngot  %q\nwant %q", guard, wantGuard)
	}
}

func TestXMLEncoderHeaderFooter(t *testing.T) {
	enc := xmlEncoder{}
	meta := RoomMeta{RoomID: 12345, ShortID: 678, Uname: `主播"<名>"`, Title: "标题&更多"}
	start := time.Date(2026, 8, 8, 15, 4, 5, 0, time.FixedZone("CST", 8*3600))
	got := string(enc.AppendFooter(enc.AppendHeader(nil, meta, start)))
	for _, want := range []string{
		`<?xml version="1.0" encoding="utf-8"?>`,
		"<i>\n",
		"<chatserver>chat.bilibili.com</chatserver>",
		`<BililiveRecorder version="`,
		`<BililiveRecorderRecordInfo roomid="12345"`,
		`shortid="678"`,
		`name="主播&quot;&lt;名&gt;&quot;"`,
		`title="标题&amp;更多"`,
		`start_time="2026-08-08T15:04:05+08:00"`,
		"</i>\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "BilirecRecordInfo") {
		t.Errorf("legacy BilirecRecordInfo should not appear:\n%s", got)
	}
}

func TestJSONLEncoderLines(t *testing.T) {
	enc := jsonlEncoder{}
	meta := RoomMeta{RoomID: 1, ShortID: 0, Uname: `n"ame`, Title: "t"}
	start := time.Date(2026, 8, 8, 15, 4, 5, 0, time.FixedZone("CST", 8*3600))
	header := string(enc.AppendHeader(nil, meta, start))
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSuffix(header, "\n")), &m); err != nil {
		t.Fatalf("meta json: %v\n%s", err, header)
	}
	if m["type"] != "meta" || m["room_id"].(float64) != 1 || m["name"] != `n"ame` {
		t.Errorf("meta = %v", m)
	}

	line := string(enc.AppendDanmaku(nil, Danmaku{
		Text: `测试"弹幕"\`, UID: 123, Uname: "u",
		Mode: 1, FontSize: 25, Color: 16777215, SendTime: 0,
	}, "1.500"))
	var dm map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSuffix(line, "\n")), &dm); err != nil {
		t.Fatalf("danmaku json: %v\n%s", err, line)
	}
	if dm["type"] != "danmaku" || dm["ts"].(float64) != 1.5 || dm["uid"].(float64) != 123 {
		t.Errorf("danmaku = %v", dm)
	}
	if dm["text"] != `测试"弹幕"\` {
		t.Errorf("text = %v", dm["text"])
	}
	sc := SuperChat{UID: 1, Price: 30, Time: 60, Message: "hi", BackgroundColor: "#EDF5FF", BackgroundBottomColor: "#2A60B2"}
	sc.UserInfo.Uname = "sc"
	sc.UserInfo.Face = "https://example.com/face.jpg"
	sc.UserInfo.NameColor = "#2C3B4A"
	scLine := string(enc.AppendSuperChat(nil, sc, "2.0"))
	if !strings.HasPrefix(scLine, `{"type":"super_chat"`) {
		t.Errorf("type should be first key:\n%s", scLine)
	}
	var scMap map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSuffix(scLine, "\n")), &scMap); err != nil {
		t.Fatalf("sc json: %v\n%s", err, scLine)
	}
	if scMap["background_color"] != "#EDF5FF" || scMap["background_bottom_color"] != "#2A60B2" {
		t.Errorf("sc colors = %v", scMap)
	}
	if scMap["user"] != "sc" || scMap["face"] != "https://example.com/face.jpg" || scMap["name_color"] != "#2C3B4A" {
		t.Errorf("sc user/face/name = %v", scMap)
	}
	// Full blivedm payload keys must not leak into the wire line.
	for _, banned := range []string{"user_info", "medal_info", "token", "dmscore", "uid"} {
		if _, ok := scMap[banned]; ok {
			t.Errorf("sc should not write %q: %v", banned, scMap)
		}
	}

	giftLine := string(enc.AppendGift(nil, Gift{
		Uname: "g", GiftName: "小花花", Num: 2, Face: "f", NameColor: "#fff",
		GiftID: 1, Price: 100, TotalCoin: 200, Action: "投喂",
	}, "3.0"))
	if !strings.HasPrefix(giftLine, `{"type":"gift"`) {
		t.Errorf("gift type first:\n%s", giftLine)
	}
	var giftMap map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSuffix(giftLine, "\n")), &giftMap); err != nil {
		t.Fatalf("gift json: %v", err)
	}
	if giftMap["gift_name"] != "小花花" || giftMap["gift_count"].(float64) != 2 {
		t.Errorf("gift = %v", giftMap)
	}
	for _, banned := range []string{"giftId", "price", "total_coin", "action", "uid"} {
		if _, ok := giftMap[banned]; ok {
			t.Errorf("gift should not write %q: %v", banned, giftMap)
		}
	}

	guardLine := string(enc.AppendGuard(nil, Guard{
		Username: "舰长", GuardLevel: 3, Num: 1, Price: 198000, GiftID: 1,
	}, "4.0"))
	if !strings.HasPrefix(guardLine, `{"type":"guard"`) {
		t.Errorf("guard type first:\n%s", guardLine)
	}
	var guardMap map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSuffix(guardLine, "\n")), &guardMap); err != nil {
		t.Fatalf("guard json: %v", err)
	}
	if guardMap["level"].(float64) != 3 || guardMap["count"].(float64) != 1 || guardMap["user"] != "舰长" {
		t.Errorf("guard = %v", guardMap)
	}
	for _, banned := range []string{"price", "gift_id", "gift_name", "uid", "guard_level"} {
		if _, ok := guardMap[banned]; ok {
			t.Errorf("guard should not write %q: %v", banned, guardMap)
		}
	}

	if !strings.HasPrefix(line, `{"type":"danmaku"`) {
		t.Errorf("danmaku type first:\n%s", line)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Error("missing trailing newline")
	}
	if len(enc.AppendFooter(nil)) != 0 {
		t.Error("jsonl footer should be empty")
	}
}
