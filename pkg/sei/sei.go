package sei

// Codec identifies the video elementary stream for SEI NAL header parsing.
type Codec int

const (
	AVC Codec = 1
	HEVC Codec = 2
)

// ForEachRBSP walks SEI NAL units in nals (AVCC length-prefixed or Annex-B).
// fn receives RBSP bytes that still include emulation-prevention bytes; slices alias nals.
func ForEachRBSP(nals []byte, codec Codec, fn func(rbsp []byte)) {
	if len(nals) == 0 || fn == nil {
		return
	}
	if isAnnexB(nals) {
		foreachAnnexB(nals, codec, fn)
		return
	}
	foreachAVCC(nals, codec, fn)
}

// UserDataUnregistered parses SEI RBSP and returns payload type 5 bodies (UUID + trailing bytes).
func UserDataUnregistered(rbsp []byte) [][]byte {
	return userDataUnregisteredFromRBSP(rbsp)
}
