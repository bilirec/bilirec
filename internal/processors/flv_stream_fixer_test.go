package processors

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/logger"
)

func TestFlvStreamFixerProcessor_LogsTimestampJumpWarning(t *testing.T) {
	fixer := flv.NewRealtimeFixer()
	defer fixer.Close()

	processor := &FlvStreamFixerProcessor{fixer: fixer, own: false}

	var logBuffer bytes.Buffer
	color := false
	logger.Init(logger.Options{Output: &logBuffer, Level: logger.WarnLevel, Color: &color})
	t.Cleanup(func() {
		logger.SetOutput(io.Discard)
		logger.SetLevel(logger.InfoLevel)
	})
	log := logger.Named("test")

	if err := processor.Open(context.Background(), log); err != nil {
		t.Fatalf("open processor: %v", err)
	}

	defer func() {
		if err := processor.Close(); err != nil {
			t.Fatalf("close processor: %v", err)
		}
	}()

	if _, err := processor.Process(context.Background(), log, flv.FlvHeader); err != nil {
		t.Fatalf("unexpected header error: %v", err)
	}

	tag1 := flv.NewTagBytes(flv.TagTypeAudio, []byte{0xaf, 0x01, 0x11})
	setTimestamp(tag1, 0)
	tag2 := flv.NewTagBytes(flv.TagTypeAudio, []byte{0xaf, 0x01, 0x22})
	setTimestamp(tag2, 1200)

	in := make([]byte, 0, flv.PrevTagSizeBytes+len(tag1)+len(tag2))
	in = append(in, 0, 0, 0, 0)
	in = append(in, tag1...)
	in = append(in, tag2...)

	if _, err := processor.Process(context.Background(), log, in); err != nil {
		t.Fatalf("unexpected process error: %v", err)
	}

	logs := logBuffer.String()
	if !strings.Contains(logs, "检测到 FLV 时间戳跳变") {
		t.Fatalf("expected warning log, got: %s", logs)
	}
	if !strings.Contains(logs, "delta=1200ms") {
		t.Fatalf("expected delta in warning log, got: %s", logs)
	}
}

func setTimestamp(tag []byte, timestamp uint32) {
	tag[4] = byte(timestamp >> 16)
	tag[5] = byte(timestamp >> 8)
	tag[6] = byte(timestamp)
	tag[7] = byte(timestamp >> 24)
}
