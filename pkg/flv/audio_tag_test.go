package flv_test

import (
	"testing"

	"github.com/bilirec/bilirec/pkg/flv"
)

func TestNewFileHeaderBytesWithFlags(t *testing.T) {
	hdr := flv.NewFileHeaderBytesWithFlags(flv.TypeFlagAudio)
	if flv.FileHeaderTypeFlags(hdr) != flv.TypeFlagAudio {
		t.Fatalf("flags=%#x", flv.FileHeaderTypeFlags(hdr))
	}
	if flv.FileHeaderHasVideo(hdr) {
		t.Fatal("audio-only header must not claim video")
	}
	av := flv.NewFileHeaderBytes()
	if flv.FileHeaderTypeFlags(av) != flv.TypeFlagAudioVideo {
		t.Fatalf("default flags=%#x", flv.FileHeaderTypeFlags(av))
	}
}

func TestClassifyAudioTag(t *testing.T) {
	isAAC, isSeq := flv.ClassifyAudioTag([]byte{0xaf, 0x00, 0x12})
	if !isAAC || !isSeq {
		t.Fatal("expected AAC sequence header")
	}
	isAAC, isSeq = flv.ClassifyAudioTag([]byte{0xaf, 0x01, 0x11})
	if !isAAC || isSeq {
		t.Fatal("expected AAC raw frame")
	}
	if isAAC, _ := flv.ClassifyAudioTag([]byte{0x2f, 0x01}); isAAC {
		t.Fatal("non-AAC sound format")
	}
}
