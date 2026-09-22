package webhook

// EventType matches BililiveRecorder Webhook v2 event names.
type EventType string

const (
	EventSessionStarted EventType = "SessionStarted"
	EventSessionEnded   EventType = "SessionEnded"
	EventFileOpening    EventType = "FileOpening"
	EventFileClosed     EventType = "FileClosed"
	EventStreamStarted  EventType = "StreamStarted"
	EventStreamEnded    EventType = "StreamEnded"
)

// SegmentMeta is persisted on convert tasks spawned by the recorder.
type SegmentMeta struct {
	SessionID          string
	FileOpenTime       string // RFC3339Nano
	RoomID             int
	ShortID            int64
	Name               string
	Title              string
	AreaNameParent     string
	AreaNameChild      string
	RecordDanmaku      bool
	DanmakuConnected   bool
	Recording          bool
	Streaming          bool
}

// envelope is the BililiveRecorder Webhook v2 POST body.
type envelope struct {
	EventType      EventType   `json:"EventType"`
	EventTimestamp string      `json:"EventTimestamp"`
	EventID        string      `json:"EventId"`
	EventData      interface{} `json:"EventData"`
}

type roomEventData struct {
	SessionID          string `json:"SessionId,omitempty"`
	RoomID             int    `json:"RoomId"`
	ShortID            int64  `json:"ShortId"`
	Name               string `json:"Name"`
	Title              string `json:"Title"`
	AreaNameParent     string `json:"AreaNameParent"`
	AreaNameChild      string `json:"AreaNameChild"`
	Recording          bool   `json:"Recording"`
	Streaming          bool   `json:"Streaming"`
	DanmakuConnected   bool   `json:"DanmakuConnected"`
	RelativePath       string `json:"RelativePath,omitempty"`
	FileOpenTime       string `json:"FileOpenTime,omitempty"`
	FileCloseTime      string `json:"FileCloseTime,omitempty"`
	FileSize           int64  `json:"FileSize,omitempty"`
	Duration           float64 `json:"Duration,omitempty"`
}
