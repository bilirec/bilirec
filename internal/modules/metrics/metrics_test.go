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
	if e.set != nil {
		t.Fatal("disabled exporter must have nil set")
	}
	// All methods must be safe no-ops.
	e.AddStreamBytes(123, 1024)
	e.RecordingStarted(123, "uname")
	e.RecordingStopped(123)
	e.SetLiveStatus(123, "uname", true)
	e.LiveSessionDetected(123)
	e.AddRecovery(123)
	e.DeleteRoom(123)
}

func TestExporterEnabled(t *testing.T) {
	e := newExporter(t, true)

	e.RecordingStarted(123, "主播A")
	e.AddStreamBytes(123, 1024)
	e.SetLiveStatus(123, "主播A", true)
	e.LiveSessionDetected(123)
	e.AddRecovery(123)

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
	e.SetLiveStatus(123, "主播B", false)

	out = e.scrape()
	for _, want := range []string{
		`bilirec_room_recording_active{room_id="123"} 0`,
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

func (e *Exporter) scrape() string {
	var buf bytes.Buffer
	e.set.WritePrometheus(&buf)
	return buf.String()
}
