package flv_test

import (
	"testing"

	"github.com/bilirec/bilirec/pkg/flv"
)

func TestClassifyVideoTag(t *testing.T) {
	t.Parallel()

	enhancedSeq := []byte{0x90, 'h', 'v', 'c', '1', 0x01, 0x02}
	enhancedHev1 := []byte{0x90, 'h', 'e', 'v', '1', 0x01}
	enhancedCoded := []byte{0x91, 'h', 'v', 'c', '1', 0xaa}
	enhancedUnknown := []byte{0x90, 'a', 'v', '0', '1', 0x01}
	avcSeq := []byte{0x17, 0x00, 0x00, 0x00, 0x00, 0x01}
	avcNALU := []byte{0x17, 0x01, 0x00, 0x00, 0x00, 0xaa}
	hevc12Seq := []byte{0x1c, 0x00, 0x00, 0x00, 0x00, 0x01}
	hevc12NALU := []byte{0x2c, 0x01, 0x00, 0x00, 0x00, 0xbb}

	tests := []struct {
		name       string
		tagData    []byte
		wantKind   flv.VideoKind
		wantHeader bool
		wantKey    bool
	}{
		{name: "empty", tagData: nil, wantKind: flv.VideoKindNone},
		{name: "short legacy", tagData: []byte{0x17}, wantKind: flv.VideoKindNone},
		{name: "short enhanced 0x90", tagData: []byte{0x90}, wantKind: flv.VideoKindNone},
		{name: "short enhanced fourcc", tagData: []byte{0x90, 'h', 'v', 'c'}, wantKind: flv.VideoKindNone},
		{name: "avc sequence header", tagData: avcSeq, wantKind: flv.VideoKindAVC, wantHeader: true, wantKey: true},
		{name: "avc nalu", tagData: avcNALU, wantKind: flv.VideoKindAVC, wantHeader: false, wantKey: true},
		{name: "hevc-12 sequence header", tagData: hevc12Seq, wantKind: flv.VideoKindHEVCLegacy, wantHeader: true, wantKey: true},
		{name: "hevc-12 nalu", tagData: hevc12NALU, wantKind: flv.VideoKindHEVCLegacy, wantHeader: false, wantKey: false},
		{name: "enhanced hvc1 sequence start", tagData: enhancedSeq, wantKind: flv.VideoKindHEVCEnhanced, wantHeader: true, wantKey: true},
		{name: "enhanced hev1 sequence start", tagData: enhancedHev1, wantKind: flv.VideoKindHEVCEnhanced, wantHeader: true, wantKey: true},
		{name: "enhanced hvc1 coded frames", tagData: enhancedCoded, wantKind: flv.VideoKindHEVCEnhanced, wantHeader: false, wantKey: true},
		{name: "enhanced unknown fourcc", tagData: enhancedUnknown, wantKind: flv.VideoKindNone, wantHeader: false, wantKey: true},
		{name: "legacy unknown codec", tagData: []byte{0x12, 0x00}, wantKind: flv.VideoKindNone, wantHeader: false, wantKey: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			kind, isHeader, isKeyframe := flv.ClassifyVideoTag(tt.tagData)
			if kind != tt.wantKind || isHeader != tt.wantHeader || isKeyframe != tt.wantKey {
				t.Fatalf("ClassifyVideoTag(%x) = kind=%d header=%v key=%v; want kind=%d header=%v key=%v",
					tt.tagData, kind, isHeader, isKeyframe, tt.wantKind, tt.wantHeader, tt.wantKey)
			}
		})
	}
}

func TestClassifyVideoTag_EnhancedNotLegacyCodecZero(t *testing.T) {
	t.Parallel()
	// 0x90 as legacy would be CodecID=0; must be Enhanced HEVC header instead.
	kind, isHeader, isKeyframe := flv.ClassifyVideoTag([]byte{0x90, 'h', 'v', 'c', '1'})
	if kind != flv.VideoKindHEVCEnhanced || !isHeader || !isKeyframe {
		t.Fatalf("got kind=%d header=%v key=%v", kind, isHeader, isKeyframe)
	}
}
