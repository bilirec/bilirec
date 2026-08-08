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

	sc := string(enc.AppendSuperChat(nil, SuperChat{
		Uname: "sc用户", UID: 123, Price: 30, Time: 60, Message: "醒目<留言>",
	}, "2.250"))
	wantSC := `<sc ts="2.250" user="sc用户" uid="123" price="30" time="60">醒目&lt;留言&gt;</sc>` + "\n"
	if sc != wantSC {
		t.Errorf("sc:\ngot  %q\nwant %q", sc, wantSC)
	}

	gift := string(enc.AppendGift(nil, Gift{
		Uname: "礼物用户", UID: 456, GiftName: "小花花", GiftCount: 2,
	}, "3.000"))
	wantGift := `<gift ts="3.000" user="礼物用户" uid="456" giftname="小花花" giftcount="2"/>` + "\n"
	if gift != wantGift {
		t.Errorf("gift:\ngot  %q\nwant %q", gift, wantGift)
	}

	guard := string(enc.AppendGuard(nil, Guard{
		Uname: "舰长用户", UID: 789, Level: 3, Count: 1,
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
		`roomid="12345"`,
		`name="主播&quot;&lt;名&gt;&quot;"`,
		`title="标题&amp;更多"`,
		`start_time="2026-08-08T15:04:05+08:00"`,
		"</i>\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
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
	if !strings.HasSuffix(line, "\n") {
		t.Error("missing trailing newline")
	}
	if len(enc.AppendFooter(nil)) != 0 {
		t.Error("jsonl footer should be empty")
	}
}
