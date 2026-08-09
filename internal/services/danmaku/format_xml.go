package danmaku

import (
	"strconv"
	"time"

	"github.com/bilirec/bilirec/pkg/updatecheck"
	recxml "github.com/bilirec/bilirec/pkg/xml"
)

type xmlEncoder struct{}

func (xmlEncoder) Ext() string { return ".xml" }

const xmlFileHeaderStatic = `<i>
<chatserver>chat.bilibili.com</chatserver>
<chatid>0</chatid>
<mission>0</mission>
<maxlimit>1000</maxlimit>
<state>0</state>
<real_name>0</real_name>
<source>0</source>
`

// xmlRecorderVersion is written on <BililiveRecorder version="..."/>.
// DanmakuFactory detects BililiveRecorder XML by scanning for that tag name
// (SC price ×1000); the value itself is informational.
func xmlRecorderVersion() string {
	if v := updatecheck.Current(); v != "" {
		return v
	}
	return "bilirec"
}

func (xmlEncoder) AppendHeader(buf []byte, meta RoomMeta, start time.Time) []byte {
	buf = append(buf, `<?xml version="1.0" encoding="utf-8"?>`...)
	buf = append(buf, '\n')
	buf = append(buf, xmlFileHeaderStatic...)
	// L1 BililiveRecorder compat: tag name required by DanmakuFactory / 录播姬 merger.
	buf = append(buf, `<BililiveRecorder version="`...)
	buf = recxml.AppendEscapedSanitized(buf, xmlRecorderVersion())
	buf = append(buf, "\"/>\n"...)
	buf = append(buf, `<BililiveRecorderRecordInfo roomid="`...)
	buf = strconv.AppendInt(buf, meta.RoomID, 10)
	buf = append(buf, `" shortid="`...)
	buf = strconv.AppendInt(buf, meta.ShortID, 10)
	buf = append(buf, `" name="`...)
	buf = recxml.AppendEscapedSanitized(buf, meta.Uname)
	buf = append(buf, `" title="`...)
	buf = recxml.AppendEscapedSanitized(buf, meta.Title)
	buf = append(buf, `" start_time="`...)
	buf = append(buf, start.Format(time.RFC3339)...)
	buf = append(buf, "\"/>\n"...)
	return buf
}

func (xmlEncoder) AppendFooter(buf []byte) []byte {
	return append(buf, "</i>\n"...)
}

func (xmlEncoder) AppendDanmaku(buf []byte, e Danmaku, ts string) []byte {
	buf = append(buf, `<d p="`...)
	buf = append(buf, ts...)
	buf = append(buf, ',')
	buf = strconv.AppendInt(buf, e.Mode, 10)
	buf = append(buf, ',')
	buf = strconv.AppendInt(buf, e.FontSize, 10)
	buf = append(buf, ',')
	buf = strconv.AppendInt(buf, e.Color, 10)
	buf = append(buf, ',')
	buf = strconv.AppendInt(buf, e.SendTime, 10)
	buf = append(buf, `,0,`...)
	buf = strconv.AppendInt(buf, e.UID, 10)
	buf = append(buf, `,0" user="`...)
	buf = recxml.AppendEscapedSanitized(buf, e.Uname)
	buf = append(buf, `">`...)
	buf = recxml.AppendEscapedSanitized(buf, e.Text)
	buf = append(buf, "</d>\n"...)
	return buf
}

func (xmlEncoder) AppendSuperChat(buf []byte, e SuperChat, ts string) []byte {
	buf = append(buf, `<sc ts="`...)
	buf = append(buf, ts...)
	buf = append(buf, `" user="`...)
	buf = recxml.AppendEscapedSanitized(buf, e.Uname())
	buf = append(buf, `" uid="`...)
	buf = strconv.AppendInt(buf, e.UID, 10)
	buf = append(buf, `" price="`...)
	buf = strconv.AppendInt(buf, e.Price, 10)
	buf = append(buf, `" time="`...)
	buf = strconv.AppendInt(buf, e.Time, 10)
	buf = append(buf, `" background_color="`...)
	buf = recxml.AppendEscapedSanitized(buf, e.BackgroundColor)
	buf = append(buf, `" background_bottom_color="`...)
	buf = recxml.AppendEscapedSanitized(buf, e.BackgroundBottomColor)
	buf = append(buf, `" background_price_color="`...)
	buf = recxml.AppendEscapedSanitized(buf, e.BackgroundPriceColor)
	buf = append(buf, `" message_font_color="`...)
	buf = recxml.AppendEscapedSanitized(buf, e.MessageFontColor)
	buf = append(buf, `" background_image="`...)
	buf = recxml.AppendEscapedSanitized(buf, e.BackgroundImage)
	buf = append(buf, `" name_color="`...)
	buf = recxml.AppendEscapedSanitized(buf, e.UserInfo.NameColor)
	buf = append(buf, `">`...)
	buf = recxml.AppendEscapedSanitized(buf, e.Message)
	buf = append(buf, "</sc>\n"...)
	return buf
}

func (xmlEncoder) AppendGift(buf []byte, e Gift, ts string) []byte {
	buf = append(buf, `<gift ts="`...)
	buf = append(buf, ts...)
	buf = append(buf, `" user="`...)
	buf = recxml.AppendEscapedSanitized(buf, e.Uname)
	buf = append(buf, `" uid="`...)
	buf = strconv.AppendInt(buf, e.UID, 10)
	buf = append(buf, `" giftname="`...)
	buf = recxml.AppendEscapedSanitized(buf, e.GiftName)
	buf = append(buf, `" giftcount="`...)
	buf = strconv.AppendInt(buf, e.Num, 10)
	buf = append(buf, "\"/>\n"...)
	return buf
}

func (xmlEncoder) AppendGuard(buf []byte, e Guard, ts string) []byte {
	buf = append(buf, `<guard ts="`...)
	buf = append(buf, ts...)
	buf = append(buf, `" user="`...)
	buf = recxml.AppendEscapedSanitized(buf, e.Username)
	buf = append(buf, `" uid="`...)
	buf = strconv.AppendInt(buf, e.UID, 10)
	buf = append(buf, `" level="`...)
	buf = strconv.AppendInt(buf, e.GuardLevel, 10)
	buf = append(buf, `" count="`...)
	buf = strconv.AppendInt(buf, e.Num, 10)
	buf = append(buf, "\"/>\n"...)
	return buf
}
