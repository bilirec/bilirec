package subtitle

import (
	"bytes"
	"encoding/binary"

	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/hls"
)

// fmp4State 在 init 段（ftyp/moov）解析后保存视频轨 ID、各轨 timescale、codec，供后续 moof 使用。
type fmp4State struct {
	VideoTrackID    uint32
	Timescale       uint32 // 视频轨 mdhd timescale；仅作 TrackTimescales 缺轨时的回退
	TrackTimescales map[uint32]uint32
	Codec           flv.VideoKind
	trexDur         map[uint32]uint32
	trexSize        map[uint32]uint32
}

func (st *fmp4State) timescaleForTrack(trackID uint32) uint32 {
	if st.TrackTimescales != nil {
		if ts, ok := st.TrackTimescales[trackID]; ok && ts > 0 {
			return ts
		}
	}
	if st.Timescale > 0 {
		return st.Timescale
	}
	return 1000
}

// accessUnitsFromFMP4Chunk 处理 fMP4 媒体分片：init 只更新 state；moof+mdat 按 trun 切 sample。
func accessUnitsFromFMP4Chunk(seg []byte, st *fmp4State) []accessUnit {
	if len(seg) < 8 {
		return nil
	}
	if fmp4SegmentHasBox(seg, "ftyp") || fmp4SegmentHasBox(seg, "moov") {
		parseInit(seg, st)
	}
	if !fmp4SegmentHasBox(seg, "moof") {
		return nil
	}
	aus := samplesFromMoof(seg, st)
	if !accessUnitsHaveSEICues(aus) && bytes.Contains(seg, subtitleUUID) {
		if au, ok := embeddedAUFromFMP4Segment(seg, st); ok {
			aus = append(aus, au)
		}
	}
	return aus
}

func embeddedAUFromFMP4Segment(seg []byte, st *fmp4State) (accessUnit, bool) {
	pts := firstMoofPTSMs(seg, st)
	if len(cuesFromEmbeddedUUID(seg, pts)) == 0 {
		return accessUnit{}, false
	}
	return accessUnit{PTSMs: pts, Codec: st.Codec, NALs: seg}, true
}

func firstMoofPTSMs(seg []byte, st *fmp4State) int64 {
	for off := 0; off < len(seg); {
		size, typ, _, ok := hls.ReadBoxHeader(seg, off)
		if !ok {
			break
		}
		end := off + size
		if typ != "moof" {
			off = end
			continue
		}
		for o := off + 8; o < end; {
			sz, boxTyp, _, ok2 := hls.ReadBoxHeader(seg, o)
			if !ok2 {
				break
			}
			e := o + sz
			if boxTyp == "traf" {
				var trackID uint32
				var base uint64
				for p := o + 8; p < e; {
					ps, pt, _, pok := hls.ReadBoxHeader(seg, p)
					if !pok {
						break
					}
					pe := p + ps
					switch pt {
					case "tfhd":
						if id, ok := hls.TfhdTrackID(seg, p, pe); ok {
							trackID = id
						}
					case "tfdt":
						base = tfdtValue(seg, p, pe)
					}
					p = pe
				}
				if trackID == 0 {
					o = e
					continue
				}
				if st.VideoTrackID != 0 && trackID != st.VideoTrackID {
					o = e
					continue
				}
				ts := st.timescaleForTrack(trackID)
				return int64(base * 1000 / uint64(ts))
			}
			o = e
		}
		off = end
	}
	return 0
}

func fmp4SegmentHasBox(seg []byte, typ string) bool {
	for off := 0; off < len(seg); {
		size, boxTyp, _, ok := hls.ReadBoxHeader(seg, off)
		if !ok {
			break
		}
		if boxTyp == typ {
			return true
		}
		off += size
	}
	return false
}

func parseInit(seg []byte, st *fmp4State) {
	st.VideoTrackID = 0
	st.Timescale = 1000
	st.TrackTimescales = make(map[uint32]uint32)
	st.Codec = flv.VideoKindAVC
	var fallbackTrack uint32
	var fallbackTS uint32
	var fallbackCodec flv.VideoKind
	for off := 0; off < len(seg); {
		size, typ, _, ok := hls.ReadBoxHeader(seg, off)
		if !ok {
			break
		}
		end := off + size
		if typ == "moov" || typ == "mvex" {
			parseInitBoxes(seg, off+8, end, st, &fallbackTrack, &fallbackTS, &fallbackCodec)
		}
		off = end
	}
	if st.VideoTrackID == 0 && fallbackTrack != 0 && fallbackTrack < 256 {
		st.VideoTrackID = fallbackTrack
		if fallbackTS > 0 {
			st.Timescale = fallbackTS
			st.TrackTimescales[fallbackTrack] = fallbackTS
		}
		if fallbackCodec != flv.VideoKindNone {
			st.Codec = fallbackCodec
		}
	}
	if st.VideoTrackID >= 256 {
		st.VideoTrackID = 0
	}
}

func parseInitBoxes(seg []byte, start, end int, st *fmp4State, fallbackTrack *uint32, fallbackTS *uint32, fallbackCodec *flv.VideoKind) {
	for off := start; off < end; {
		size, typ, _, ok := hls.ReadBoxHeader(seg, off)
		if !ok {
			break
		}
		boxEnd := off + size
		switch typ {
		case "trak":
			trackID, timescale, codec, isVideo := trakInfo(seg, off, boxEnd)
			if trackID == 0 {
				break
			}
			if timescale > 0 {
				st.TrackTimescales[trackID] = timescale
			}
			if codec != flv.VideoKindNone && isVideo {
				st.Codec = codec
			}
			if isVideo && trackID < 256 {
				st.VideoTrackID = trackID
				if timescale > 0 {
					st.Timescale = timescale
				}
			} else if *fallbackTrack == 0 && trackID < 256 {
				*fallbackTrack = trackID
				*fallbackTS = timescale
				*fallbackCodec = codec
			}
		case "trex":
			parseTrexBox(seg, off, boxEnd, st)
		case "moov", "mvex":
			parseInitBoxes(seg, off+8, boxEnd, st, fallbackTrack, fallbackTS, fallbackCodec)
		}
		off = boxEnd
	}
}

func parseTrexBox(data []byte, start, end int, st *fmp4State) {
	if end-start < 28 {
		return
	}
	trackID := binary.BigEndian.Uint32(data[start+12 : start+16])
	if trackID == 0 {
		return
	}
	dur := binary.BigEndian.Uint32(data[start+20 : start+24])
	size := binary.BigEndian.Uint32(data[start+24 : start+28])
	if st.trexDur == nil {
		st.trexDur = make(map[uint32]uint32)
	}
	if st.trexSize == nil {
		st.trexSize = make(map[uint32]uint32)
	}
	if dur > 0 {
		st.trexDur[trackID] = dur
	}
	if size > 0 {
		st.trexSize[trackID] = size
	}
}

func trakInfo(data []byte, start, end int) (uint32, uint32, flv.VideoKind, bool) {
	var trackID uint32
	var timescale uint32
	var codec flv.VideoKind
	var isVideo bool
	for off := start + 8; off < end; {
		size, typ, _, ok := hls.ReadBoxHeader(data, off)
		if !ok {
			break
		}
		boxEnd := off + size
		switch typ {
		case "mdia":
			ts, c, v := mdiaInfo(data, off, boxEnd)
			if ts > 0 {
				timescale = ts
			}
			if c != flv.VideoKindNone {
				codec = c
			}
			isVideo = v
		case "tkhd":
			if id, ok := hls.TkhdTrackID(data, off, boxEnd); ok {
				trackID = id
			}
		}
		off = boxEnd
	}
	return trackID, timescale, codec, isVideo
}

func mdiaInfo(data []byte, start, end int) (uint32, flv.VideoKind, bool) {
	var timescale uint32
	var codec flv.VideoKind
	isVideo := false
	for o := start + 8; o < end; {
		sz, t, _, ok2 := hls.ReadBoxHeader(data, o)
		if !ok2 {
			break
		}
		e := o + sz
		switch t {
		case "mdhd":
			if ts, ok := mdhdTimescale(data, o, e); ok {
				timescale = ts
			}
		case "hdlr":
			if e-o >= 20 {
				isVideo = string(data[o+16:o+20]) == "vide"
			}
		case "minf":
			if c := codecFromMinf(data, o, e); c != flv.VideoKindNone {
				codec = c
			}
		}
		o = e
	}
	return timescale, codec, isVideo
}

func mdhdTimescale(data []byte, start, end int) (uint32, bool) {
	if end-start < 20 {
		return 0, false
	}
	if data[start+8] == 1 {
		if end-start < 32 {
			return 0, false
		}
		return binary.BigEndian.Uint32(data[start+28 : start+32]), true
	}
	return binary.BigEndian.Uint32(data[start+20 : start+24]), true
}

func codecFromMinf(data []byte, start, end int) flv.VideoKind {
	for off := start + 8; off < end; {
		size, typ, _, ok := hls.ReadBoxHeader(data, off)
		if !ok {
			break
		}
		boxEnd := off + size
		if typ == "stsd" {
			// stsd 后 8 字节为 version/flags/entry_count，之后是 sample entry（avc1/hvc1 等）。
			if boxEnd-off > 16 {
				entry := data[off+16 : boxEnd]
				if len(entry) >= 12 {
					codecName := string(entry[4:8])
					switch codecName {
					case "avc1", "avc3":
						return flv.VideoKindAVC
					case "hvc1", "hev1":
						return flv.VideoKindHEVCEnhanced
					}
				}
			}
		}
		off = boxEnd
	}
	return flv.VideoKindNone
}

func samplesFromMoof(seg []byte, st *fmp4State) []accessUnit {
	var out []accessUnit
	for off := 0; off < len(seg); {
		size, typ, _, ok := hls.ReadBoxHeader(seg, off)
		if !ok {
			break
		}
		moofEnd := off + size
		if typ != "moof" {
			off = moofEnd
			continue
		}
		mdat, mdatPayloadStart := mdatPayloadAfter(seg, moofEnd)
		if len(mdat) == 0 {
			off = moofEnd
			continue
		}
		for trafOff := off + 8; trafOff < moofEnd; {
			trafSize, trafTyp, _, trafOK := hls.ReadBoxHeader(seg, trafOff)
			if !trafOK {
				break
			}
			trafEnd := trafOff + trafSize
			if trafTyp == "traf" {
				aus := samplesFromTraf(seg, trafOff, trafEnd, st, mdat, off, mdatPayloadStart)
				out = append(out, aus...)
			}
			trafOff = trafEnd
		}
		off = moofEnd
	}
	return out
}

func mdatPayloadAfter(seg []byte, moofEnd int) ([]byte, int) {
	if moofEnd+8 > len(seg) {
		return nil, 0
	}
	size, typ, hdr, ok := hls.ReadBoxHeader(seg, moofEnd)
	if !ok || typ != "mdat" {
		return nil, 0
	}
	payloadStart := moofEnd + hdr
	payloadEnd := moofEnd + size
	if payloadEnd > len(seg) || payloadStart > payloadEnd {
		return nil, 0
	}
	return seg[payloadStart:payloadEnd], payloadStart
}

func samplesFromTraf(data []byte, trafStart, trafEnd int, st *fmp4State, mdat []byte, moofStart, mdatPayloadStart int) []accessUnit {
	trackID := uint32(0)
	var base uint64
	var trunStart, trunEnd int
	var tfhdStart, tfhdEnd int
	for off := trafStart + 8; off < trafEnd; {
		size, typ, hdr, ok := hls.ReadBoxHeader(data, off)
		if !ok {
			break
		}
		end := off + size
		switch typ {
		case "tfhd":
			tfhdStart, tfhdEnd = off, end
			if id, ok := hls.TfhdTrackID(data, off, end); ok {
				trackID = id
			}
		case "tfdt":
			base = tfdtValue(data, off, end)
		case "trun":
			trunStart, trunEnd = off+hdr, end
		}
		off = end
	}
	if trunEnd <= trunStart || len(mdat) == 0 || trackID == 0 {
		return nil
	}
	if st.VideoTrackID != 0 && trackID != st.VideoTrackID {
		return nil
	}
	defDur, hasDefDur, defSize, hasDefSize := tfhdDefaults(data, tfhdStart, tfhdEnd)
	aus := parseTrun(data, trunStart, trunEnd, base, st, trackID, mdat, moofStart, mdatPayloadStart, defDur, hasDefDur, defSize, hasDefSize)
	if frag, ok := fragmentAUFromTraf(data, trunStart, trunEnd, base, st, trackID, mdat, moofStart, mdatPayloadStart); ok {
		if !accessUnitsHaveSEICues(aus) {
			aus = append(aus, frag)
		}
	}
	adoptVideoTrackID(st, trackID, aus)
	return aus
}

func accessUnitsHaveSEICues(aus []accessUnit) bool {
	for _, au := range aus {
		if len(cuesFromSEI(au)) > 0 {
			return true
		}
	}
	return false
}

// fragmentAUFromTraf：B 站 fMP4 常在 trun 里不写 sample_size，整段视频 mdat（从 data_offset 起）仍是合法 AVCC，适合扫 SEI。
func fragmentAUFromTraf(data []byte, trunStart, trunEnd int, base uint64, st *fmp4State, trackID uint32, mdat []byte, moofStart, mdatPayloadStart int) (accessUnit, bool) {
	off, ok := trunDataOffset(data, trunStart, trunEnd)
	if !ok || off <= 0 {
		return accessUnit{}, false
	}
	abs := moofStart + int(off)
	if abs < mdatPayloadStart {
		return accessUnit{}, false
	}
	cursor := abs - mdatPayloadStart
	if cursor >= len(mdat) || !bytes.Contains(mdat[cursor:], subtitleUUID) {
		return accessUnit{}, false
	}
	ts := st.timescaleForTrack(trackID)
	ptsMs := int64(base * 1000 / uint64(ts))
	return accessUnit{PTSMs: ptsMs, Codec: st.Codec, NALs: mdat[cursor:]}, true
}

func trunDataOffset(data []byte, start, end int) (int32, bool) {
	if end-start < 8 {
		return 0, false
	}
	flags := uint32(data[start+1])<<16 | uint32(data[start+2])<<8 | uint32(data[start+3])
	if flags&0x1 == 0 {
		return 0, false
	}
	if start+12 > end {
		return 0, false
	}
	return int32(binary.BigEndian.Uint32(data[start+8 : start+12])), true
}

// adoptVideoTrackID：fMP4 init 常只有 mvex/trex 无 trak，从首个带字幕 SEI 的 tfhd 学习视频轨 ID。
func adoptVideoTrackID(st *fmp4State, trackID uint32, aus []accessUnit) {
	if st.VideoTrackID != 0 || trackID == 0 {
		return
	}
	for _, au := range aus {
		if len(cuesFromSEI(au)) > 0 {
			st.VideoTrackID = trackID
			return
		}
	}
}

// tfhdDefaults 读取 default_sample_duration / default_sample_size（trun 未带 per-sample 字段时使用）。
func tfhdDefaults(data []byte, start, end int) (defaultDur uint32, hasDur bool, defaultSize uint32, hasSize bool) {
	if start <= 0 || end-start < 16 {
		return 0, false, 0, false
	}
	flags := uint32(data[start+9])<<16 | uint32(data[start+10])<<8 | uint32(data[start+11])
	off := start + 16
	if flags&0x1 != 0 {
		off += 8
	}
	if flags&0x2 != 0 {
		off += 4
	}
	if flags&0x8 != 0 {
		if off+4 > end {
			return 0, false, 0, false
		}
		defaultDur = binary.BigEndian.Uint32(data[off : off+4])
		hasDur = true
		off += 4
	}
	if flags&0x10 != 0 {
		if off+4 > end {
			return defaultDur, hasDur, 0, false
		}
		defaultSize = binary.BigEndian.Uint32(data[off : off+4])
		hasSize = true
	}
	return defaultDur, hasDur, defaultSize, hasSize
}

func tfdtValue(data []byte, start, end int) uint64 {
	if end-start < 16 {
		return 0
	}
	if data[start+8] == 1 && end-start >= 20 {
		return binary.BigEndian.Uint64(data[start+12 : start+20])
	}
	return uint64(binary.BigEndian.Uint32(data[start+12 : start+16]))
}

// parseTrun 按 trun flags 读每个 sample 的 duration/size/cto，从 mdat 切出 AVCC NAL 并算 PTS（毫秒）。
func parseTrun(data []byte, start, end int, base uint64, st *fmp4State, trackID uint32, mdat []byte, moofStart, mdatPayloadStart int, defDur uint32, hasDefDur bool, defSize uint32, hasDefSize bool) []accessUnit {
	if end-start < 8 {
		return nil
	}
	flags := uint32(data[start+1])<<16 | uint32(data[start+2])<<8 | uint32(data[start+3])
	sampleCount := int(binary.BigEndian.Uint32(data[start+4 : start+8]))
	off := start + 8
	var dataOffset int32
	if flags&0x1 != 0 {
		if off+4 > end {
			return nil
		}
		dataOffset = int32(binary.BigEndian.Uint32(data[off : off+4]))
		off += 4
	}
	firstSampleFlagsPresent := flags&0x4 != 0
	if firstSampleFlagsPresent {
		off += 4
	}
	ts := st.timescaleForTrack(trackID)
	abs := moofStart + int(dataOffset)
	if abs < mdatPayloadStart {
		return nil
	}
	cursor := abs - mdatPayloadStart
	var out []accessUnit
	pts := base
	for i := 0; i < sampleCount && off < end; i++ {
		var dur uint32
		if flags&0x100 != 0 {
			if off+4 > end {
				break
			}
			dur = binary.BigEndian.Uint32(data[off : off+4])
			off += 4
		} else if hasDefDur {
			dur = defDur
		} else if st.trexDur != nil {
			dur = st.trexDur[trackID]
		}
		var sampleSize uint32
		if flags&0x200 != 0 {
			if off+4 > end {
				break
			}
			sampleSize = binary.BigEndian.Uint32(data[off : off+4])
			off += 4
		} else if hasDefSize {
			sampleSize = defSize
		} else if st.trexSize != nil {
			sampleSize = st.trexSize[trackID]
		}
		if flags&0x400 != 0 {
			off += 4
		}
		var cto int32
		if flags&0x800 != 0 {
			if off+4 > end {
				break
			}
			cto = int32(binary.BigEndian.Uint32(data[off : off+4]))
			off += 4
		}
		if sampleSize == 0 && cursor+4 <= len(mdat) {
			ln := binary.BigEndian.Uint32(mdat[cursor : cursor+4])
			if ln > 0 && cursor+4+int(ln) <= len(mdat) {
				sampleSize = 4 + ln
			}
		}
		if sampleSize == 0 && sampleCount == 1 && cursor < len(mdat) {
			sampleSize = uint32(len(mdat) - cursor)
		}
		if sampleSize == 0 {
			pts += uint64(dur)
			continue
		}
		if cursor+int(sampleSize) > len(mdat) {
			break
		}
		sample := mdat[cursor : cursor+int(sampleSize)]
		cursor += int(sampleSize)
		ptsMs := int64((pts + uint64(cto)) * 1000 / uint64(ts))
		out = append(out, accessUnit{PTSMs: ptsMs, Codec: st.Codec, NALs: sample})
		pts += uint64(dur)
	}
	return out
}
