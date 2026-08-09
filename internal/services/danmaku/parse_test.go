package danmaku

import (
	"strings"
	"testing"
	"time"
)

func TestParseDanmaku(t *testing.T) {
	e, ok := parseDanmaku([]byte(testDanmakuJSON))
	if !ok {
		t.Fatal("expected ok")
	}
	if e.Text != `测试<弹幕>&"'` || e.UID != 123456 || e.Uname != "测试用户" {
		t.Errorf("unexpected parse: %+v", e)
	}
	if e.Mode != 1 || e.FontSize != 25 || e.Color != 16777215 || e.SendTime != 1754668800000 {
		t.Errorf("unexpected attrs: %+v", e)
	}
}

func TestParseDanmakuDefaults(t *testing.T) {
	raw := `{"cmd":"DANMU_MSG","info":[[],"文本",[0,""]]}`
	e, ok := parseDanmaku([]byte(raw))
	if !ok {
		t.Fatal("expected ok")
	}
	if e.Mode != 1 || e.FontSize != 25 || e.Color != 16777215 || e.SendTime != 0 {
		t.Errorf("defaults not applied: %+v", e)
	}
}

func TestParseDanmakuMissingInfo(t *testing.T) {
	if _, ok := parseDanmaku([]byte(`{"cmd":"DANMU_MSG"}`)); ok {
		t.Error("expected !ok for missing info")
	}
}

func TestParseSuperChatGiftGuard(t *testing.T) {
	sc := parseSuperChat([]byte(`{"cmd":"SUPER_CHAT_MESSAGE","data":{"uid":123,"id":999,"start_time":1700000000,"end_time":1700000060,"user_info":{"uname":"sc用户","name_color":"#646c7a","face":"https://example.com/a.png"},"price":30,"time":60,"message":"醒目留言","background_color":"#EDF5FF","background_bottom_color":"#2A60B2","background_price_color":"#7497CD","message_font_color":"#FFFFFF","background_image":"https://example.com/bg.png","background_color_start":"#111111","background_color_end":"#222222"}}`))
	if sc.UID != 123 || sc.Price != 30 || sc.Message != "醒目留言" || sc.Uname() != "sc用户" {
		t.Errorf("sc: %+v", sc)
	}
	if sc.BackgroundColor != "#EDF5FF" || sc.BackgroundBottomColor != "#2A60B2" || sc.BackgroundPriceColor != "#7497CD" || sc.MessageFontColor != "#FFFFFF" {
		t.Errorf("sc colors: %+v", sc)
	}
	if sc.ID != 999 || sc.StartTime != 1700000000 || sc.EndTime != 1700000060 || sc.UserInfo.NameColor != "#646c7a" || sc.UserInfo.Face == "" || sc.BackgroundImage == "" {
		t.Errorf("sc meta: %+v", sc)
	}
	gift := parseGift([]byte(`{"cmd":"SEND_GIFT","data":{"uname":"礼物用户","uid":456,"giftName":"小花花","num":2,"price":100,"coin_type":"gold"}}`))
	if gift.UID != 456 || gift.GiftName != "小花花" || gift.Num != 2 || gift.Price != 100 || gift.CoinType != "gold" {
		t.Errorf("gift: %+v", gift)
	}
	guard := parseGuard([]byte(`{"cmd":"GUARD_BUY","data":{"username":"舰长用户","uid":789,"guard_level":3,"num":1,"price":198000,"gift_name":"舰长"}}`))
	if guard.UID != 789 || guard.GuardLevel != 3 || guard.Num != 1 || guard.Price != 198000 || guard.GiftName != "舰长" {
		t.Errorf("guard: %+v", guard)
	}
}

func TestFormatRelativeTS(t *testing.T) {
	start := time.Now()
	if got := formatRelativeTS(start, start.Add(1500*time.Millisecond)); got != "1.500" {
		t.Errorf("got %q, want 1.500", got)
	}
	if got := formatRelativeTS(start, start.Add(-time.Second)); got != "0.000" {
		t.Errorf("negative duration should clamp to 0.000, got %q", got)
	}
	if got := formatRelativeTS(start, start); got != "0.000" {
		t.Errorf("got %q, want 0.000", got)
	}
}

func TestNewFormatEncoder(t *testing.T) {
	for _, format := range []string{"jsonl", "xml"} {
		enc, err := NewFormatEncoder(format)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if !strings.HasPrefix(enc.Ext(), ".") {
			t.Errorf("%s ext = %q", format, enc.Ext())
		}
	}
	if _, err := NewFormatEncoder("yaml"); err == nil {
		t.Error("expected error for unsupported format")
	}
}

func TestXMLEncoderRoundTrip(t *testing.T) {
	enc, _ := NewFormatEncoder("xml")
	e, _ := parseDanmaku([]byte(testDanmakuJSON))
	got := string(enc.AppendDanmaku(nil, e, "1.500"))
	want := `<d p="1.500,1,25,16777215,1754668800000,0,123456,0" user="测试用户">测试&lt;弹幕&gt;&amp;&quot;&#39;</d>` + "\n"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
