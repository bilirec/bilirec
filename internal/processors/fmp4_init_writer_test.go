package processors

import (
	"context"
	"testing"

	"github.com/bilirec/bilirec/pkg/logger"
)

func TestFmp4InitWriter_PrependsPendingInit(t *testing.T) {
	initSeg := makeInitSegment()
	moof := makeMediaFragment()

	p := &fmp4InitWriterProcessor{pendingInit: initSeg}
	ctx := context.Background()
	log := logger.Nop()

	if err := p.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	out, err := p.Process(ctx, log, moof)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if len(out) != len(initSeg)+len(moof) {
		t.Fatalf("expected combined size %d, got %d", len(initSeg)+len(moof), len(out))
	}
	if string(out[4:8]) != "ftyp" {
		t.Fatalf("expected ftyp at start, got %q", string(out[4:8]))
	}
	moofOff := len(initSeg)
	if string(out[moofOff+4:moofOff+8]) != "moof" {
		t.Fatalf("expected moof after init, got %q", string(out[moofOff+4:moofOff+8]))
	}

	second, err := p.Process(ctx, log, moof)
	if err != nil {
		t.Fatalf("second Process failed: %v", err)
	}
	if len(second) != len(moof) {
		t.Fatalf("expected second chunk unchanged, got len %d", len(second))
	}
}

func TestFmp4InitWriter_NoOpWhenPendingEmpty(t *testing.T) {
	moof := makeMediaFragment()
	p := &fmp4InitWriterProcessor{}
	ctx := context.Background()
	log := logger.Nop()

	if err := p.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	out, err := p.Process(ctx, log, moof)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if len(out) != len(moof) {
		t.Fatalf("expected unchanged moof, got len %d", len(out))
	}
}

func TestFmp4InitWriter_EmptyInputDoesNotMarkWritten(t *testing.T) {
	initSeg := makeInitSegment()
	moof := makeMediaFragment()
	p := &fmp4InitWriterProcessor{pendingInit: initSeg}
	ctx := context.Background()
	log := logger.Nop()

	if err := p.Open(ctx, log); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	if _, err := p.Process(ctx, log, []byte{}); err != nil {
		t.Fatalf("empty Process failed: %v", err)
	}

	out, err := p.Process(ctx, log, moof)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if len(out) != len(initSeg)+len(moof) {
		t.Fatalf("expected prepend after empty input, got len %d", len(out))
	}
}
