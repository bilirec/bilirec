package metrics

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"time"

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
	if e.Enabled() {
		t.Fatal("disabled exporter must report Enabled false")
	}
	// All methods must be safe no-ops.
	if e.StreamSession(123) != nil {
		t.Fatal("disabled exporter must return a nil stream session")
	}
	e.AddStreamBytes(123, 1024)
	e.RecordingStarted(123, "uname")
	e.RecordingStopped(123)
	e.SetLiveStatus(123, "uname", true)
	e.LiveSessionDetected(123)
	e.AddRecovery(123)
	e.StreamConnectionActive(123, true)
	e.StreamConnectionActive(123, false)
	e.RecordingRecovering(123, true)
	e.RecordingRecovering(123, false)
	e.StreamConnectAttempt(123)
	e.RecordingRotation(123)
	e.RecordingStartFailed(123, ReasonDisk)
	e.RecordingStartFailed(123, "bogus")
	e.RecordingRecoverySucceeded(123)
	e.RecordingGaveUp(123, ReasonMaxAttempts)
	e.RecordingGaveUp(123, "bogus")
	e.RecordingSegmentDiscarded(123, ReasonTiny)
	e.RecordingPipelineError(123, ReasonNotFLV)
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
	e.SetConvertTasksPending("ffmpeg", 1)
	e.SetConvertTasksProcessing("ffmpeg", 1)
	e.DeleteRoom(123)
}

func TestVictoriaLogsMetricsRecordEvents(t *testing.T) {
	registry := newRoomRegistry()
	e := &Exporter{set: registry.set, registry: registry}

	e.AddVictoriaLogsQueueBytes(128)
	e.VictoriaLogsLogDropped()
	e.VictoriaLogsRequestFailed()
	e.VictoriaLogsRetry()

	out := e.scrape()
	for _, want := range []string{
		"bilirec_victorialogs_queue_bytes 128",
		"bilirec_victorialogs_logs_dropped_total 1",
		"bilirec_victorialogs_requests_failed_total 1",
		"bilirec_victorialogs_retries_total 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in scrape output:\n%s", want, out)
		}
	}
}

func TestConvertTaskProcessingGauge(t *testing.T) {
	registry := newRoomRegistry()
	e := &Exporter{set: registry.set, registry: registry}

	e.SetConvertTasksProcessing("ffmpeg", 2)
	e.SetConvertTasksProcessing("cloudconvert", 1)

	out := e.scrape()
	for _, want := range []string{
		`bilirec_convert_tasks_processing{provider="ffmpeg"} 2`,
		`bilirec_convert_tasks_processing{provider="cloudconvert"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in scrape output:\n%s", want, out)
		}
	}

	e.SetConvertTasksProcessing("ffmpeg", 0)
	e.SetConvertTasksProcessing("cloudconvert", 0)

	out = e.scrape()
	for _, want := range []string{
		`bilirec_convert_tasks_processing{provider="ffmpeg"} 0`,
		`bilirec_convert_tasks_processing{provider="cloudconvert"} 0`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in scrape output:\n%s", want, out)
		}
	}
}

func TestConvertTasksPendingGauge(t *testing.T) {
	registry := newRoomRegistry()
	e := &Exporter{set: registry.set, registry: registry}

	e.SetConvertTasksPending("ffmpeg", 2)
	e.SetConvertTasksPending("cloudconvert", 1)

	out := e.scrape()
	for _, want := range []string{
		`bilirec_convert_tasks_pending{provider="ffmpeg"} 2`,
		`bilirec_convert_tasks_pending{provider="cloudconvert"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in scrape output:\n%s", want, out)
		}
	}

	e.SetConvertTasksPending("ffmpeg", 0)
	if out = e.scrape(); !strings.Contains(out, `bilirec_convert_tasks_pending{provider="ffmpeg"} 0`) {
		t.Errorf("pending gauge was not updated:\n%s", out)
	}
}

func TestExporterEnabled(t *testing.T) {
	e := newExporter(t, true)
	if !e.Enabled() {
		t.Fatal("enabled exporter must report Enabled true")
	}

	e.RecordingStarted(123, "主播A")
	e.AddStreamBytes(123, 1024)
	sess := e.StreamSession(123)
	if sess == nil {
		t.Fatal("enabled exporter must return a stream session")
	}
	sess.AddBytesWritten(900)
	sess.RecordTimestampJump(1200)
	sess.RecordEnqueueWait(50 * time.Millisecond)
	sess.RecordSlowFlush()
	sess.RecordSlowSync()
	e.SetLiveStatus(123, "主播A", true)
	e.LiveSessionDetected(123)
	e.AddRecovery(123)
	e.StreamConnectionActive(123, true)
	e.StreamConnectAttempt(123)
	e.RecordingRotation(123)
	e.RecordingStartFailed(123, ReasonDisk)
	e.RecordingRecoverySucceeded(123)
	e.RecordingGaveUp(123, ReasonMaxAttempts)
	e.RecordingSegmentDiscarded(123, ReasonTiny)
	e.RecordingPipelineError(123, ReasonNotFLV)
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
		`bilirec_room_stream_bytes_written_total{room_id="123"} 900`,
		`bilirec_room_stream_timestamp_jumps_total{room_id="123"} 1`,
		`bilirec_room_stream_timestamp_jump_collapsed_seconds_total{room_id="123"} 1.2`,
		`bilirec_room_stream_enqueue_waits_total{room_id="123"} 1`,
		`bilirec_room_stream_slow_flushes_total{room_id="123"} 1`,
		`bilirec_room_stream_slow_syncs_total{room_id="123"} 1`,
		`bilirec_room_recording_sessions_total{room_id="123"} 1`,
		`bilirec_room_recording_active{room_id="123"} 1`,
		`bilirec_room_recording_recovering{room_id="123"} 0`,
		`bilirec_room_live_status{room_id="123"} 1`,
		`bilirec_room_live_sessions_total{room_id="123"} 1`,
		`bilirec_room_stream_recovery_total{room_id="123"} 1`,
		`bilirec_room_stream_connection_active{room_id="123"} 1`,
		`bilirec_room_stream_connect_attempts_total{room_id="123"} 1`,
		`bilirec_room_recording_rotations_total{room_id="123"} 1`,
		`bilirec_room_recording_start_failures_total{room_id="123",reason="disk"} 1`,
		`bilirec_room_stream_recovery_success_total{room_id="123"} 1`,
		`bilirec_room_recording_gave_up_total{room_id="123",reason="max_attempts"} 1`,
		`bilirec_room_recording_segments_discarded_total{room_id="123",reason="tiny"} 1`,
		`bilirec_room_recording_pipeline_errors_total{room_id="123",reason="not_flv"} 1`,
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
	if runtime.GOOS == "linux" {
		for _, want := range []string{
			`process_resident_memory_anon_bytes`,
			`process_resident_memory_file_bytes`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in scrape output:\n%s", want, out)
			}
		}
	}

	// Stop recording, go offline, and rename: gauges self-heal, old info series replaced.
	e.RecordingStopped(123)
	e.DanmakuSessionStopped(123)
	e.SetLiveStatus(123, "主播B", false)

	out = e.scrape()
	for _, want := range []string{
		`bilirec_room_recording_active{room_id="123"} 0`,
		`bilirec_room_recording_recovering{room_id="123"} 0`,
		`bilirec_room_stream_connection_active{room_id="123"} 0`,
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

func TestStreamSession_EagerCountersAndEnqueueThreshold(t *testing.T) {
	e := newExporter(t, true)
	sess := e.StreamSession(789)
	if sess == nil {
		t.Fatal("expected non-nil session")
	}

	sess.RecordEnqueueWait(500 * time.Microsecond)
	out := e.scrape()
	if strings.Contains(out, `bilirec_room_stream_enqueue_waits_total{room_id="789"} 1`) {
		t.Fatalf("StreamSession should ignore sub-threshold waits:\n%s", out)
	}

	sess.AddBytesWritten(100)
	sess.RecordEnqueueWait(15 * time.Millisecond)
	sess.RecordTimestampJump(1200)
	out = e.scrape()
	for _, want := range []string{
		`bilirec_room_stream_bytes_written_total{room_id="789"} 100`,
		`bilirec_room_stream_enqueue_waits_total{room_id="789"} 1`,
		`bilirec_room_stream_timestamp_jumps_total{room_id="789"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in scrape output:\n%s", want, out)
		}
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
	e.StreamConnectionActive(123, true)
	e.StreamConnectAttempt(123)
	e.RecordingRotation(123)
	e.RecordingStartFailed(123, ReasonAPI)
	e.RecordingRecoverySucceeded(123)
	e.RecordingGaveUp(123, ReasonNotLiveTimeout)
	e.RecordingSegmentDiscarded(123, ReasonSkippedSmall)
	e.RecordingSegmentDiscarded(123, ReasonStatError)
	e.RecordingPipelineError(123, ReasonOpen)
	e.RecordingPipelineError(123, ReasonOther)
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

func TestRecordingReasonBlacklist(t *testing.T) {
	registry := newRoomRegistry()
	e := &Exporter{
		set:      registry.set,
		registry: registry,
	}

	e.RecordingStartFailed(7, "")
	e.RecordingStartFailed(7, "disk-full")
	e.RecordingGaveUp(7, "")
	e.RecordingGaveUp(7, "timeout")
	e.RecordingSegmentDiscarded(7, "too_small")
	e.RecordingPipelineError(7, "write")

	if out := e.scrape(); strings.Contains(out, `room_id="7"`) {
		t.Fatalf("unknown reasons must not create series:\n%s", out)
	}

	e.RecordingStartFailed(7, ReasonOther)
	e.RecordingGaveUp(7, ReasonEncrypted)
	e.RecordingSegmentDiscarded(7, ReasonTiny)
	e.RecordingPipelineError(7, ReasonNotFLV)

	out := e.scrape()
	for _, want := range []string{
		`bilirec_room_recording_start_failures_total{room_id="7",reason="other"} 1`,
		`bilirec_room_recording_gave_up_total{room_id="7",reason="encrypted"} 1`,
		`bilirec_room_recording_segments_discarded_total{room_id="7",reason="tiny"} 1`,
		`bilirec_room_recording_pipeline_errors_total{room_id="7",reason="not_flv"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in scrape output:\n%s", want, out)
		}
	}

	e.UnregisterRecorderRoom(7)
	out = e.scrape()
	for _, want := range []string{
		`bilirec_room_recording_start_failures_total{room_id="7",reason="other"} 1`,
		`bilirec_room_recording_gave_up_total{room_id="7",reason="encrypted"} 1`,
		`bilirec_room_recording_segments_discarded_total{room_id="7",reason="tiny"} 1`,
		`bilirec_room_recording_pipeline_errors_total{room_id="7",reason="not_flv"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("UnregisterRecorderRoom must keep reason counters, missing %q:\n%s", want, out)
		}
	}

	e.DeleteRoom(7)
	if out = e.scrape(); strings.Contains(out, `room_id="7"`) {
		t.Fatalf("DeleteRoom should drop reason series:\n%s", out)
	}
}

func TestRecorderCountersSurviveUnregister(t *testing.T) {
	registry := newRoomRegistry()
	e := &Exporter{
		set:      registry.set,
		registry: registry,
	}

	e.RecordingStarted(123, "主播")
	e.AddStreamBytes(123, 1024)
	e.StreamConnectAttempt(123)
	e.RecordingStopped(123)
	e.UnregisterRecorderRoom(123)

	out := e.scrape()
	for _, want := range []string{
		`bilirec_room_recording_sessions_total{room_id="123"} 1`,
		`bilirec_room_stream_bytes_total{room_id="123"} 1024`,
		`bilirec_room_stream_connect_attempts_total{room_id="123"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing surviving counter %q:\n%s", want, out)
		}
	}
	for _, gone := range []string{
		`bilirec_room_recording_active{room_id="123"}`,
		`bilirec_room_recording_recovering{room_id="123"}`,
		`bilirec_room_stream_connection_active{room_id="123"}`,
	} {
		if strings.Contains(out, gone) {
			t.Errorf("gauge %q should be unregistered:\n%s", gone, out)
		}
	}

	e.RecordingStarted(123, "主播")
	out = e.scrape()
	if !strings.Contains(out, `bilirec_room_recording_sessions_total{room_id="123"} 2`) {
		t.Fatalf("second start should increment surviving counter:\n%s", out)
	}
	if !strings.Contains(out, `bilirec_room_recording_active{room_id="123"} 1`) {
		t.Fatalf("second start should recreate recording_active:\n%s", out)
	}
	if !strings.Contains(out, `bilirec_room_recording_recovering{room_id="123"} 0`) {
		t.Fatalf("second start should recreate recording_recovering at 0:\n%s", out)
	}
}

func TestRecordingRecoveringGaugeLifecycle(t *testing.T) {
	registry := newRoomRegistry()
	e := &Exporter{
		set:      registry.set,
		registry: registry,
	}

	e.RecordingStarted(123, "主播")
	out := e.scrape()
	if !strings.Contains(out, `bilirec_room_recording_recovering{room_id="123"} 0`) {
		t.Fatalf("RecordingStarted should create recovering gauge at 0:\n%s", out)
	}

	e.RecordingRecovering(123, true)
	out = e.scrape()
	if !strings.Contains(out, `bilirec_room_recording_recovering{room_id="123"} 1`) {
		t.Fatalf("RecordingRecovering(true) should set gauge to 1:\n%s", out)
	}

	e.RecordingRecovering(123, false)
	out = e.scrape()
	if !strings.Contains(out, `bilirec_room_recording_recovering{room_id="123"} 0`) {
		t.Fatalf("RecordingRecovering(false) should set gauge to 0:\n%s", out)
	}

	e.RecordingRecovering(123, true)
	e.RecordingStopped(123)
	out = e.scrape()
	if !strings.Contains(out, `bilirec_room_recording_recovering{room_id="123"} 0`) {
		t.Fatalf("RecordingStopped should clear recovering gauge:\n%s", out)
	}

	e.UnregisterRecorderRoom(123)
	out = e.scrape()
	if strings.Contains(out, `bilirec_room_recording_recovering{room_id="123"}`) {
		t.Fatalf("UnregisterRecorderRoom should drop recovering gauge:\n%s", out)
	}
}

func TestDanmakuCountersSurviveUnregister(t *testing.T) {
	registry := newRoomRegistry()
	e := &Exporter{
		set:      registry.set,
		registry: registry,
	}

	e.DanmakuSessionStarted(123)
	e.DanmakuConnectionAttempt(123)
	e.DanmakuConnectionActive(123, true)
	e.DanmakuMessageReceived(123, eventTypeDanmaku)
	e.AddDanmakuBytes(123, 64)
	e.DanmakuSessionStopped(123)
	e.UnregisterDanmakuRoom(123)

	out := e.scrape()
	for _, want := range []string{
		`bilirec_danmaku_sessions_total{room_id="123"} 1`,
		`bilirec_danmaku_connection_attempts_total{room_id="123"} 1`,
		`bilirec_danmaku_messages_total{room_id="123",event_type="danmaku"} 1`,
		`bilirec_danmaku_bytes_total{room_id="123"} 64`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing surviving counter %q:\n%s", want, out)
		}
	}
	for _, gone := range []string{
		`bilirec_danmaku_recording_active{room_id="123"}`,
		`bilirec_danmaku_connection_active{room_id="123"}`,
	} {
		if strings.Contains(out, gone) {
			t.Errorf("gauge %q should be unregistered:\n%s", gone, out)
		}
	}

	e.DanmakuSessionStarted(123)
	out = e.scrape()
	if !strings.Contains(out, `bilirec_danmaku_sessions_total{room_id="123"} 2`) {
		t.Fatalf("second start should increment surviving counter:\n%s", out)
	}
	if !strings.Contains(out, `bilirec_danmaku_recording_active{room_id="123"} 1`) {
		t.Fatalf("second start should recreate danmaku_recording_active:\n%s", out)
	}
}

func (e *Exporter) scrape() string {
	var buf bytes.Buffer
	e.set.WritePrometheus(&buf)
	return buf.String()
}
