package flv

// TypeFlagVideo and TypeFlagAudio are FLV file header TypeFlags bits.
const (
	TypeFlagVideo       = 0x01
	TypeFlagAudio       = 0x04
	TypeFlagAudioVideo  = TypeFlagVideo | TypeFlagAudio
)

// ClassifyAudioTag inspects a complete FLV audio tag payload.
// For Bilibili HTTP-FLV, AAC uses sound format 10 (tagData[0]>>4) and
// AACPacketType 0 for sequence header, 1 for raw AAC frame.
func ClassifyAudioTag(tagData []byte) (isAAC, isSequenceHeader bool) {
	if len(tagData) < 2 {
		return false, false
	}
	if (tagData[0] >> 4) != 10 {
		return false, false
	}
	return true, tagData[1] == 0x00
}
