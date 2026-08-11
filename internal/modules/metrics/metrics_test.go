package metrics

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/config"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func newExporter(t *testing.T, enabled bool) *Exporter {
	t.Helper()
	if enabled {
		t.Setenv("METRICS_ENABLED", "true")
		t.Setenv("METRICS_PORT", "0") // ephemeral port
	}
	var e *Exporter
	app := fxtest.New(t,
		config.Module,
		Module,
		fx.Populate(&e),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	return e
}

func TestExporterDisabledNoop(t *testing.T) {
	e := newExporter(t, false)
	if e.registry != nil {
		t.Fatal("disabled exporter must have nil registry")
	}
	// All methods must be safe no-ops.
	e.AddStreamBytes(123, 1024)
	e.RecordingStarted(123, "uname")
	e.RecordingStopped(123)
	e.SetLiveStatus(123, "uname", true)
	e.LiveSessionDetected(123)
	e.AddRecovery(123)
	e.DanmakuSessionStarted(123)
	e.DanmakuSessionStopped(123)
	e.DanmakuConnectionAttempt(123)
	e.DanmakuConnectionActive(123, true)
	e.DanmakuConnectionActive(123, false)
	e.DanmakuReconnect(123)
	e.DanmakuMessageReceived(123, eventTypeDanmaku)
	e.DanmakuMessageDropped(123, eventTypeDanmaku)
	e.DanmakuParseError(123)
	e.AddDanmakuBytes(123, 1)
	e.DanmakuRotation(123)
	e.DanmakuRotationDropped(123)
	e.DeleteRoom(123)
}

func TestExporterEnabled(t *testing.T) {
	e := newExporter(t, true)

	e.RecordingStarted(123, "主播A")
	e.AddStreamBytes(123, 1024)
	e.SetLiveStatus(123, "主播A", true)
	e.LiveSessionDetected(123)
	e.AddRecovery(123)
	e.DanmakuSessionStarted(123)
	e.DanmakuConnectionAttempt(123)
	e.DanmakuConnectionActive(123, true)
	e.DanmakuReconnect(123)
	e.DanmakuMessageReceived(123, eventTypeDanmaku)
	e.DanmakuMessageDropped(123, eventTypeSuperChat)
	e.DanmakuParseError(123)
	e.AddDanmakuBytes(123, 1024)
	e.DanmakuRotation(123)
	e.DanmakuRotationDropped(123)

	out := e.scrape()
	for _, want := range []string{
		`bilirec_room_stream_bytes_total{room_id="123"} 1024`,
		`bilirec_room_recording_sessions_total{room_id="123"} 1`,
		`bilirec_room_recording_active{room_id="123"} 1`,
		`bilirec_room_live_status{room_id="123"} 1`,
		`bilirec_room_live_sessions_total{room_id="123"} 1`,
		`bilirec_room_stream_recovery_total{room_id="123"} 1`,
		`bilirec_room_info{room_id="123",uname="主播A"} 1`,
		`bilirec_active_recordings 1`,
		`bilirec_danmaku_recording_active{room_id="123"} 1`,
		`bilirec_danmaku_sessions_total{room_id="123"} 1`,
		`bilirec_danmaku_connection_active{room_id="123"} 1`,
		`bilirec_danmaku_connection_attempts_total{room_id="123"} 1`,
		`bilirec_danmaku_reconnects_total{room_id="123"} 1`,
		`bilirec_danmaku_messages_total{room_id="123",event_type="danmaku"} 1`,
		`bilirec_danmaku_messages_dropped_total{room_id="123",event_type="super_chat"} 1`,
		`bilirec_danmaku_parse_errors_total{room_id="123"} 1`,
		`bilirec_danmaku_bytes_total{room_id="123"} 1024`,
		`bilirec_danmaku_rotations_total{room_id="123"} 1`,
		`bilirec_danmaku_rotation_dropped_total{room_id="123"} 1`,
		`go_goroutines`,
		`process_resident_memory_bytes`,
		`process_cpu_seconds_total`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in scrape output:\n%s", want, out)
		}
	}

	// Stop recording, go offline, and rename: gauges self-heal, old info series replaced.
	e.RecordingStopped(123)
	e.DanmakuSessionStopped(123)
	e.SetLiveStatus(123, "主播B", false)

	out = e.scrape()
	for _, want := range []string{
		`bilirec_room_recording_active{room_id="123"} 0`,
		`bilirec_danmaku_recording_active{room_id="123"} 0`,
		`bilirec_danmaku_connection_active{room_id="123"} 0`,
		`bilirec_room_live_status{room_id="123"} 0`,
		`bilirec_active_recordings 0`,
		`bilirec_room_info{room_id="123",uname="主播B"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in scrape output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "主播A") {
		t.Errorf("stale room_info series for old uname should be unregistered:\n%s", out)
	}

	// DeleteRoom removes all series of the room.
	e.DeleteRoom(123)
	if out = e.scrape(); strings.Contains(out, `room_id="123"`) {
		t.Errorf("DeleteRoom should remove all series of room 123:\n%s", out)
	}
}

func TestExporterDeleteRoomClearsAllSpecs(t *testing.T) {
	registry := newRoomRegistry()
	e := &Exporter{
		set:      registry.set,
		registry: registry,
	}

	e.AddStreamBytes(123, 1)
	e.RecordingStarted(123, "主播")
	e.RecordingStopped(123)
	e.SetLiveStatus(123, "主播", false)
	e.LiveSessionDetected(123)
	e.AddRecovery(123)
	e.DanmakuSessionStarted(123)
	e.DanmakuSessionStopped(123)
	e.DanmakuConnectionAttempt(123)
	e.DanmakuConnectionActive(123, true)
	e.DanmakuReconnect(123)
	for _, eventType := range danmakuEventTypes {
		e.DanmakuMessageReceived(123, eventType)
		e.DanmakuMessageDropped(123, eventType)
	}
	e.DanmakuParseError(123)
	e.AddDanmakuBytes(123, 1)
	e.DanmakuRotation(123)
	e.DanmakuRotationDropped(123)

	if out := e.scrape(); !strings.Contains(out, `room_id="123"`) {
		t.Fatal("test setup did not create any room series")
	}

	e.DeleteRoom(123)
	if out := e.scrape(); strings.Contains(out, `room_id="123"`) {
		t.Fatalf("DeleteRoom should unregister every room series:\n%s", out)
	}
	if got := registry.counters.Size(); got != 0 {
		t.Fatalf("DeleteRoom should clear room counter cache, got %d entries", got)
	}
}

func (e *Exporter) scrape() string {
	var buf bytes.Buffer
	e.set.WritePrometheus(&buf)
	return buf.String()
}
