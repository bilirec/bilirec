package stream

import (
	"strings"
	"testing"
)

func TestParseM3u8_ValidPlaylist(t *testing.T) {
	body := strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-VERSION:3",
		"#EXT-X-MEDIA-SEQUENCE:123",
		"#EXTINF:2.0,",
		"seg-123.ts",
		"#EXTINF:2.5,",
		"seg-124.ts",
	}, "\n")

	pl, err := parseM3u8(body)
	if err != nil {
		t.Fatalf("parseM3u8 returned error: %v", err)
	}
	if pl.mediaSeq != 123 {
		t.Fatalf("unexpected mediaSeq: got=%d want=123", pl.mediaSeq)
	}
	if len(pl.segments) != 2 {
		t.Fatalf("unexpected segment count: got=%d want=2", len(pl.segments))
	}
	if pl.segments[0].uri != "seg-123.ts" || pl.segments[0].duration != 2.0 {
		t.Fatalf("unexpected first segment: %+v", pl.segments[0])
	}
	if pl.segments[1].uri != "seg-124.ts" || pl.segments[1].duration != 2.5 {
		t.Fatalf("unexpected second segment: %+v", pl.segments[1])
	}
}

func TestParseM3u8_InvalidMediaSequence(t *testing.T) {
	body := strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-MEDIA-SEQUENCE:not-a-number",
		"#EXTINF:2.0,",
		"seg.ts",
	}, "\n")

	_, err := parseM3u8(body)
	if err == nil {
		t.Fatal("expected parseM3u8 to return error for invalid media sequence")
	}
}

func TestParseM3u8_InvalidExtinfDuration(t *testing.T) {
	body := strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-MEDIA-SEQUENCE:1",
		"#EXTINF:not-a-float,",
		"seg.ts",
	}, "\n")

	_, err := parseM3u8(body)
	if err == nil {
		t.Fatal("expected parseM3u8 to return error for invalid EXTINF duration")
	}
}

func TestParseM3u8_ScannerErrorOnLongLine(t *testing.T) {
	// bufio.Scanner has a default token limit of 64K.
	veryLongLine := strings.Repeat("a", 70*1024)
	body := strings.Join([]string{
		"#EXTM3U",
		veryLongLine,
	}, "\n")

	_, err := parseM3u8(body)
	if err == nil {
		t.Fatal("expected parseM3u8 to return scanner error for oversized line")
	}
}
