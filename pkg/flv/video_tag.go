package flv

// VideoKind is the FLV video payload layout recognized from a complete tag body.
type VideoKind uint8

const (
	VideoKindNone VideoKind = iota
	VideoKindAVC
	VideoKindHEVCLegacy
	VideoKindHEVCEnhanced
)

const (
	videoExHeader            = 0x80
	codecAVC                 = 7
	codecHEVC                = 12
	packetTypeSequenceStart  = 0
	legacyFrameTypeKey       = 0x10
	legacyCodecIDMask        = 0x0F
	legacyFrameTypeMask      = 0xF0
	enhancedFrameTypeMask    = 0x70
	enhancedFrameTypeKey     = 0x10
	enhancedPacketTypeMask   = 0x0F
	minLegacyVideoTagBytes   = 2
	minEnhancedVideoTagBytes = 5
)

var (
	fourCCHvc1 = [4]byte{'h', 'v', 'c', '1'}
	fourCCHev1 = [4]byte{'h', 'e', 'v', '1'}
)

// ClassifyVideoTag inspects a complete FLV video tag payload.
// ExHeader (bit7) is checked before CodecID so Enhanced SequenceStart (e.g. 0x90)
// is never misread as legacy CodecID 0.
//
// Unknown layouts return VideoKindNone and isHeader=false.
func ClassifyVideoTag(tagData []byte) (kind VideoKind, isHeader, isKeyframe bool) {
	if len(tagData) == 0 {
		return VideoKindNone, false, false
	}

	b := tagData[0]
	if b&videoExHeader != 0 {
		if len(tagData) < minEnhancedVideoTagBytes {
			return VideoKindNone, false, false
		}
		isKeyframe = b&enhancedFrameTypeMask == enhancedFrameTypeKey
		var fourCC [4]byte
		copy(fourCC[:], tagData[1:5])
		if fourCC != fourCCHvc1 && fourCC != fourCCHev1 {
			return VideoKindNone, false, isKeyframe
		}
		return VideoKindHEVCEnhanced, b&enhancedPacketTypeMask == packetTypeSequenceStart, isKeyframe
	}

	if len(tagData) < minLegacyVideoTagBytes {
		return VideoKindNone, false, false
	}

	isKeyframe = b&legacyFrameTypeMask == legacyFrameTypeKey
	switch b & legacyCodecIDMask {
	case codecAVC:
		kind = VideoKindAVC
	case codecHEVC:
		kind = VideoKindHEVCLegacy
	default:
		return VideoKindNone, false, isKeyframe
	}
	return kind, tagData[1] == packetTypeSequenceStart, isKeyframe
}
