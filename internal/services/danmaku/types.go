package danmaku

import "encoding/json"

// Event payload types aligned with Akegarasu/blivedm-go (MIT) message package.
// Parse unmarshals the full WS `data` object into these structs; JSONL/XML
// encoders then persist only the fields needed for replay / overlays.

// MedalInfo mirrors blivedm-go medal blobs on SuperChat / Gift.
type MedalInfo struct {
	AnchorRoomid     int64  `json:"anchor_roomid"`
	AnchorUname      string `json:"anchor_uname"`
	GuardLevel       int64  `json:"guard_level"`
	IconID           int64  `json:"icon_id"`
	IsLighted        int64  `json:"is_lighted"`
	MedalColor       any    `json:"medal_color"` // string on SC, int on Gift
	MedalColorBorder int64  `json:"medal_color_border"`
	MedalColorEnd    int64  `json:"medal_color_end"`
	MedalColorStart  int64  `json:"medal_color_start"`
	MedalLevel       int64  `json:"medal_level"`
	MedalName        string `json:"medal_name"`
	Special          string `json:"special"`
	TargetID         int64  `json:"target_id"`
}

// Danmaku is a flattened DANMU_MSG record for recording / replay.
// Source paths follow blivedm-go message.Danmaku.Parse (info array + Extra).
type Danmaku struct {
	Text       string
	UID        int64
	Uname      string
	Mode       int64
	FontSize   int64
	Color      int64
	SendTime   int64
	DmType     int64 // info.0.12 — 0 text, 1 emoticon (blivedm Type)
	GuardLevel int64 // info.7
	UserLevel  int64 // info.2.16.0
	Admin      bool  // info.2.2
	Urank      int64 // info.2.5
	MobileVerify bool // info.2.6
	// Medal (info.3)
	MedalLevel  int64
	MedalName   string
	MedalUpName string
	MedalRoomID int64
	MedalColor  int64
	MedalUpUID  int64
	// Extra (info.0.15.extra) — selected fields used for replay
	DmTypeExtra    int64
	Direction      int64
	EmoticonUnique string
	EmoticonURL    string
}

// SuperChat mirrors blivedm-go message.SuperChat (data object of SUPER_CHAT_MESSAGE).
type SuperChat struct {
	BackgroundBottomColor string  `json:"background_bottom_color"`
	BackgroundColor       string  `json:"background_color"`
	BackgroundColorEnd    string  `json:"background_color_end"`
	BackgroundColorStart  string  `json:"background_color_start"`
	BackgroundIcon        string  `json:"background_icon"`
	BackgroundImage       string  `json:"background_image"`
	BackgroundPriceColor  string  `json:"background_price_color"`
	ColorPoint            float64 `json:"color_point"`
	Dmscore               int64   `json:"dmscore"`
	EndTime               int64   `json:"end_time"`
	Gift                  struct {
		GiftID   int64  `json:"gift_id"`
		GiftName string `json:"gift_name"`
		Num      int64  `json:"num"`
	} `json:"gift"`
	ID               int64     `json:"id"`
	IsRanked         int64     `json:"is_ranked"`
	IsSendAudit      int64     `json:"is_send_audit"`
	MedalInfo        MedalInfo `json:"medal_info"`
	Message          string    `json:"message"`
	MessageFontColor string    `json:"message_font_color"`
	MessageTrans     string    `json:"message_trans"`
	Price            int64     `json:"price"`
	Rate             int64     `json:"rate"`
	StartTime        int64     `json:"start_time"`
	Time             int64     `json:"time"`
	Token            string    `json:"token"`
	TransMark        int64     `json:"trans_mark"`
	Ts               int64     `json:"ts"`
	UID              int64     `json:"uid"`
	UserInfo         struct {
		Face       string `json:"face"`
		FaceFrame  string `json:"face_frame"`
		GuardLevel int64  `json:"guard_level"`
		IsMainVip  int64  `json:"is_main_vip"`
		IsSvip     int64  `json:"is_svip"`
		IsVip      int64  `json:"is_vip"`
		LevelColor string `json:"level_color"`
		Manager    int64  `json:"manager"`
		NameColor  string `json:"name_color"`
		Title      string `json:"title"`
		Uname      string `json:"uname"`
		UserLevel  int64  `json:"user_level"`
	} `json:"user_info"`
}

// Uname returns the sender display name.
func (s SuperChat) Uname() string { return s.UserInfo.Uname }

// Gift mirrors blivedm-go message.Gift (data object of SEND_GIFT).
// Opaque combo / blind bags stay as json.RawMessage so unmarshal keeps them
// without forcing a fixed nested schema into recordings.
type Gift struct {
	Action            string          `json:"action"`
	BatchComboID      string          `json:"batch_combo_id"`
	BatchComboSend    jsonRaw         `json:"batch_combo_send"`
	BeatID            string          `json:"beatId"`
	BizSource         string          `json:"biz_source"`
	BlindGift         jsonRaw         `json:"blind_gift"`
	BroadcastID       int64           `json:"broadcast_id"`
	CoinType          string          `json:"coin_type"`
	ComboResourcesID  int64           `json:"combo_resources_id"`
	ComboSend         jsonRaw         `json:"combo_send"`
	ComboStayTime     int64           `json:"combo_stay_time"`
	ComboTotalCoin    int64           `json:"combo_total_coin"`
	CritProb          int64           `json:"crit_prob"`
	Demarcation       int64           `json:"demarcation"`
	DiscountPrice     int64           `json:"discount_price"`
	Dmscore           int64           `json:"dmscore"`
	Draw              int64           `json:"draw"`
	Effect            int64           `json:"effect"`
	EffectBlock       int64           `json:"effect_block"`
	Face              string          `json:"face"`
	FloatScResourceID int64           `json:"float_sc_resource_id"`
	GiftID            int64           `json:"giftId"`
	GiftName          string          `json:"giftName"`
	GiftType          int64           `json:"giftType"`
	Gold              int64           `json:"gold"`
	GuardLevel        int64           `json:"guard_level"`
	IsFirst           bool            `json:"is_first"`
	IsSpecialBatch    int64           `json:"is_special_batch"`
	Magnification     float64         `json:"magnification"`
	MedalInfo         MedalInfo       `json:"medal_info"`
	NameColor         string          `json:"name_color"`
	Num               int64           `json:"num"`
	OriginalGiftName  string          `json:"original_gift_name"`
	Price             int64           `json:"price"`
	Rcost             int64           `json:"rcost"`
	Remain            int64           `json:"remain"`
	Rnd               string          `json:"rnd"`
	SendMaster        jsonRaw         `json:"send_master"`
	Silver            int64           `json:"silver"`
	Super             int64           `json:"super"`
	SuperBatchGiftNum int64           `json:"super_batch_gift_num"`
	SuperGiftNum      int64           `json:"super_gift_num"`
	SvgaBlock         int64           `json:"svga_block"`
	TagImage          string          `json:"tag_image"`
	Tid               string          `json:"tid"`
	Timestamp         int64           `json:"timestamp"`
	TopList           jsonRaw         `json:"top_list"`
	TotalCoin         int64           `json:"total_coin"`
	UID               int64           `json:"uid"`
	Uname             string          `json:"uname"`
}

// jsonRaw preserves opaque nested objects from the live payload.
type jsonRaw = json.RawMessage

// Guard mirrors blivedm-go message.GuardBuy (data object of GUARD_BUY).
type Guard struct {
	UID        int64  `json:"uid"`
	Username   string `json:"username"`
	GuardLevel int64  `json:"guard_level"`
	Num        int64  `json:"num"`
	Price      int64  `json:"price"`
	GiftID     int64  `json:"gift_id"`
	GiftName   string `json:"gift_name"`
	StartTime  int64  `json:"start_time"`
	EndTime    int64  `json:"end_time"`
}

// Uname returns the buyer display name (GuardBuy uses username).
func (g Guard) Uname() string { return g.Username }
