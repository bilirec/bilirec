package processors_test

import (
	"context"
	"testing"

	"github.com/eric2788/bilirec/internal/processors"
	"github.com/eric2788/bilirec/pkg/flv"
	"github.com/eric2788/bilirec/pkg/pipeline"
)

func TestFlvHeaderWriterProcessor_BootstrapsHeaders(t *testing.T) {
	videoTag := flv.NewTagBytes(flv.TagTypeVideo, []byte{0x17, 0x00, 0x00})
	audioTag := flv.NewTagBytes(flv.TagTypeAudio, []byte{0xaf, 0x00, 0x12})

	pipe := pipeline.New(
		processors.NewFlvHeaderWriter(videoTag, audioTag),
	)

	if err := pipe.Open(context.Background()); err != nil {
		t.Fatalf("open pipeline: %v", err)
	}
	defer pipe.Close()

	out, err := pipe.Process(context.Background(), []byte{0x09, 0xaa})
	if err != nil {
		t.Fatalf("process: %v", err)
	}

	want := len(flv.NewFileHeaderBytes()) + len(videoTag) + len(audioTag) + 2
	if len(out) != want {
		t.Fatalf("unexpected output length: got %d want %d", len(out), want)
	}

	// Second process: no header prefix expected
	second, err := pipe.Process(context.Background(), []byte{0x08})
	if err != nil {
		t.Fatalf("second process: %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("expected passthrough after first process, got %d bytes", len(second))
	}
}

func TestFlvHeaderWriterProcessor_NoOptionalHeaders(t *testing.T) {
	pipe := pipeline.New(
		processors.NewFlvHeaderWriter(nil, nil),
	)
	if err := pipe.Open(context.Background()); err != nil {
		t.Fatalf("open pipeline: %v", err)
	}
	defer pipe.Close()

	out, err := pipe.Process(context.Background(), []byte{0xAA, 0xBB})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	want := len(flv.NewFileHeaderBytes()) + 2
	if len(out) != want {
		t.Fatalf("expected %d bytes (file header + payload), got %d", want, len(out))
	}
}
