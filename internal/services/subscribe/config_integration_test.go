package subscribe

import (
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/room"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

// TestIntegration_RoomConfig_DeleteOldestOnLowDiskPersists verifies the room
// config field survives subscribe → UpdateConfig → GetConfig through real storage.
func TestIntegration_RoomConfig_DeleteOldestOnLowDiskPersists(t *testing.T) {
	t.Setenv("DATABASE_DIR", t.TempDir())

	const roomID = 99001

	var svc *Service
	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Provide(room.NewService),
		fx.Provide(NewService),
		fx.Populate(&svc),
		fx.StartTimeout(5*time.Second),
	)
	app.RequireStart()
	t.Cleanup(func() { app.RequireStop() })

	if err := svc.Subscribe(roomID); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	cfg := defaultRoomConfig()
	cfg.AutoRecord = true
	cfg.DeleteOldestOnLowDisk = true
	if err := svc.UpdateConfig(roomID, cfg); err != nil {
		t.Fatalf("update config: %v", err)
	}

	got, err := svc.GetConfig(roomID)
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if !got.DeleteOldestOnLowDisk {
		t.Fatal("expected DeleteOldestOnLowDisk true after persist")
	}
	if !got.AutoRecord {
		t.Fatal("expected AutoRecord true after persist")
	}
}
