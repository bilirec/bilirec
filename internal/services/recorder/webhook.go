package recorder

import (
	"time"

	"github.com/bilirec/bilirec/internal/services/webhook"
)

func (r *Service) emitSessionStarted(info *Info) {
	if r.cfg == nil || !r.cfg.WebhookConfigured() || r.wh == nil || info == nil || info.room == nil || info.sessionID == "" {
		return
	}
	dm := info.startOptions.recordDanmaku && r.dm.IsSessionActive(int(info.room.RoomID))
	r.wh.SessionStarted(info.room, info.sessionID, true, dm)
}

func (r *Service) emitSessionEnded(info *Info) {
	if r.cfg == nil || !r.cfg.WebhookConfigured() || r.wh == nil || info == nil || info.room == nil || info.sessionID == "" {
		return
	}
	dm := info.startOptions.recordDanmaku && r.dm.IsSessionActive(int(info.room.RoomID))
	r.wh.SessionEnded(info.room, info.sessionID, false, dm)
}

func (r *Service) emitFileOpening(roomID int, info *Info, absPath string) {
	if r.cfg == nil || !r.cfg.WebhookConfigured() || r.wh == nil || info == nil || info.room == nil || info.sessionID == "" || absPath == "" {
		return
	}
	fileOpen := info.segmentOpenTime
	if fileOpen.IsZero() {
		fileOpen = time.Now()
	}
	recording := r.GetStatus(roomID) == Recording
	dm := info.startOptions.recordDanmaku && r.dm.IsSessionActive(roomID)
	r.wh.FileOpening(info.room, info.sessionID, absPath, fileOpen, recording, dm)
}

func (r *Service) emitFileClosed(roomID int, info *Info, absPath string) {
	if r.cfg == nil || !r.cfg.WebhookConfigured() || r.wh == nil || info == nil || info.room == nil || info.sessionID == "" || absPath == "" {
		return
	}
	fileOpen := info.segmentOpenTime
	if fileOpen.IsZero() {
		fileOpen = time.Now()
	}
	fileClose := time.Now()
	recording := r.GetStatus(roomID) == Recording
	dm := info.startOptions.recordDanmaku && r.dm.IsSessionActive(roomID)
	r.wh.FileClosed(info.room, info.sessionID, absPath, fileOpen, fileClose, recording, dm)
}

func (r *Service) registerConvertSegmentMeta(outputPath string, info *Info) {
	if r.cfg == nil || !r.cfg.WebhookConfigured() || r.wh == nil || info == nil {
		return
	}
	meta := r.segmentWebhookMeta(info)
	if meta == nil {
		return
	}
	r.wh.RegisterConvertSegmentMeta(outputPath, meta)
}

func (r *Service) segmentWebhookMeta(info *Info) *webhook.SegmentMeta {
	if info == nil || info.room == nil || info.sessionID == "" {
		return nil
	}
	roomID := int(info.room.RoomID)
	recording := r.GetStatus(roomID) == Recording
	dm := info.startOptions.recordDanmaku && r.dm.IsSessionActive(roomID)
	open := info.segmentOpenTime
	if open.IsZero() {
		open = time.Now()
	}
	return &webhook.SegmentMeta{
		SessionID:        info.sessionID,
		FileOpenTime:     open.Format(time.RFC3339Nano),
		RoomID:           roomID,
		ShortID:          info.room.ShortID,
		Name:             info.room.Uname,
		Title:            info.room.Title,
		AreaNameParent:   info.room.ParentAreaName,
		AreaNameChild:    info.room.AreaName,
		RecordDanmaku:    info.startOptions.recordDanmaku,
		DanmakuConnected: dm,
		Recording:        recording,
		Streaming:        info.room.LiveStatus == 1,
	}
}
