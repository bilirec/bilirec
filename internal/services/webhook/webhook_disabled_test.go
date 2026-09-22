package webhook

import (
	"testing"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
)

func TestDisabledWebhookIsNoOp(t *testing.T) {
	cfg := &config.Config{WebhookURLs: ""}
	if cfg.WebhookConfigured() {
		t.Fatal("expected webhook not configured")
	}
	svc := newWebhookService(t, cfg)

	room := &bilibili.LiveRoomInfoDetail{RoomID: 1, Uname: "u", Title: "t"}
	svc.SessionStarted(room, "sid", true, true)
	svc.StreamStarted(room)
	svc.RegisterConvertSegmentMeta("/any/path.flv", &SegmentMeta{SessionID: "sid", RoomID: 1})
	svc.OnConvertTaskSuccess("/any/path.flv", "/any/path.mp4")

	if meta := svc.PopConvertSegmentMeta("/any/path.flv"); meta != nil {
		t.Fatal("disabled service should not retain convert meta")
	}
}
