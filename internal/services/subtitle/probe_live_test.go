package subtitle

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/stream"
	"github.com/bilirec/bilirec/pkg/hls"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

const probeSubtitleRoomID = 22748536

func TestLiveProbeHLSFMP4SEI(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if os.Getenv("BILIREC_SUBTITLE_PROBE") != "1" {
		t.Skip("set BILIREC_SUBTITLE_PROBE=1 for live HLS probe")
	}
	probeHLSSEI(t, bilibili.ProfileHLSFMP4, func(seg []byte, st *fmp4State) (int, int) {
		aus := 0
		cues := 0
		for _, au := range accessUnitsFromFMP4Chunk(seg, st) {
			aus++
			cues += len(cuesFromSEI(au))
		}
		return aus, cues
	})
}

func TestLiveProbeHLSTSSEI(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if os.Getenv("BILIREC_SUBTITLE_PROBE") != "1" {
		t.Skip("set BILIREC_SUBTITLE_PROBE=1 for live HLS probe")
	}
	probeHLSSEI(t, bilibili.ProfileHLSTS, func(seg []byte, _ *fmp4State) (int, int) {
		sc := &tsScratch{}
		aus := 0
		cues := 0
		for _, au := range accessUnitsFromTSChunk(seg, sc) {
			aus++
			cues += len(cuesFromSEI(au))
		}
		return aus, cues
	})
}

func probeHLSSEI(t *testing.T, profile bilibili.StreamProfile, parse func([]byte, *fmp4State) (int, int)) {
	t.Helper()
	var bili *bilibili.Client
	var st *stream.Service
	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Provide(stream.NewService),
		fx.Populate(&bili, &st),
	)
	app.RequireStart()
	defer app.RequireStop()

	enabled, err := bili.RoomSubtitleEnabled(probeSubtitleRoomID)
	if err != nil || !enabled {
		t.Skip("room subtitle not enabled")
	}

	urls, err := bili.GetStreamURLsV2(probeSubtitleRoomID, bilibili.WithProfiles(profile))
	if err != nil || len(urls) == 0 {
		t.Skipf("no stream urls: %v", err)
	}
	info := urls[0]
	for _, u := range urls {
		if u.Format == "fmp4" && profile == bilibili.ProfileHLSFMP4 {
			info = u
			break
		}
		if u.Format == "ts" && profile == bilibili.ProfileHLSTS {
			info = u
			break
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	chunkPool, release := st.AcquireChunkPool(info.Qn)
	defer release()

	fetchM3u8 := func() (string, error) {
		return info.URL, nil
	}
	ch, err := st.ReadHlsStream(fetchM3u8, bili.GetLiveHlsPlaylistClient(), bili.GetLiveHlsSegmentClient(), ctx, info.Qn, chunkPool, release)
	if err != nil {
		t.Fatalf("ReadHlsStream: %v", err)
	}

	var state fmp4State
	var chunks, totalAU, totalCues, rawUUID int
	deadline := time.After(45 * time.Second)
	for {
		select {
		case seg, ok := <-ch:
			if !ok {
				t.Fatalf("stream ended early: chunks=%d aus=%d cues=%d rawUUID=%d state=%+v", chunks, totalAU, totalCues, rawUUID, state)
			}
			chunks++
			if bytes.Contains(seg, subtitleUUID) {
				rawUUID++
			}
			if chunks == 1 {
				parseInit(seg, &state)
				t.Logf("init track=%d timescale=%d trexSize=%v trexDur=%v moov=%s", state.VideoTrackID, state.Timescale, state.trexSize, state.trexDur, describeMoovTree(seg))
			}
			if chunks <= 3 {
				t.Logf("chunk %d len=%d boxes=%s", chunks, len(seg), describeTopBoxes(seg))
				if chunks == 2 {
					t.Logf("moof debug: %s", summarizeMoofs(seg))
					t.Logf("chunk2 embed cues=%d uuid=%t", len(cuesFromEmbeddedUUID(seg, 0)), bytes.Contains(seg, subtitleUUID))
				}
			}
			aus, cues := parse(seg, &state)
			totalAU += aus
			totalCues += cues
			if chunks%15 == 0 {
				t.Logf("progress chunks=%d aus=%d cues=%d rawUUID=%d track=%d codec=%v", chunks, totalAU, totalCues, rawUUID, state.VideoTrackID, state.Codec)
			}
			if totalCues > 0 {
				t.Logf("OK chunks=%d aus=%d cues=%d rawUUID=%d state=%+v", chunks, totalAU, totalCues, rawUUID, state)
				return
			}
		case <-deadline:
			t.Fatalf("no cues in window: chunks=%d aus=%d cues=%d rawUUID=%d state=%+v", chunks, totalAU, totalCues, rawUUID, state)
		}
	}
}

func summarizeMoofs(seg []byte) string {
	var parts []string
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
		mdat, _ := mdatPayloadAfter(seg, moofEnd)
		for trafOff := off + 8; trafOff < moofEnd; {
			trafSize, trafTyp, _, trafOK := hls.ReadBoxHeader(seg, trafOff)
			if !trafOK {
				break
			}
			trafEnd := trafOff + trafSize
			if trafTyp == "traf" {
				parts = append(parts, summarizeTraf(seg, trafOff, trafEnd, len(mdat)))
			}
			trafOff = trafEnd
		}
		off = moofEnd
	}
	return strings.Join(parts, " | ")
}

func summarizeTraf(seg []byte, trafStart, trafEnd int, mdatLen int) string {
	trackID := uint32(0)
	var trunFlags uint32
	var sampleCount int
	var dataOffset int32
	hasTrun := false
	for o := trafStart + 8; o < trafEnd; {
		sz, typ, hdr, ok := hls.ReadBoxHeader(seg, o)
		if !ok {
			break
		}
		e := o + sz
		switch typ {
		case "tfhd":
			if id, ok := hls.TfhdTrackID(seg, o, e); ok {
				trackID = id
			}
		case "trun":
			hasTrun = true
			body := seg[o+hdr : e]
			if len(body) >= 12 {
				trunFlags = uint32(body[0])<<16 | uint32(body[1])<<8 | uint32(body[2])
				sampleCount = int(binary.BigEndian.Uint32(body[4:8]))
				if trunFlags&0x1 != 0 && len(body) >= 12 {
					dataOffset = int32(binary.BigEndian.Uint32(body[8:12]))
				}
			}
		}
		o = e
	}
	if !hasTrun {
		return "traf(no-trun)"
	}
	return fmt.Sprintf("traf(id=%d n=%d flags=0x%06x off=%d mdat=%d)", trackID, sampleCount, trunFlags&0xffffff, dataOffset, mdatLen)
}

func describeMoovTree(seg []byte) string {
	for off := 0; off < len(seg); {
		size, typ, _, ok := hls.ReadBoxHeader(seg, off)
		if !ok {
			break
		}
		end := off + size
		if typ == "moov" {
			return listBoxTypes(seg, off+8, end, 0)
		}
		off = end
	}
	return ""
}

func listBoxTypes(seg []byte, start, end int, depth int) string {
	if depth > 4 {
		return "..."
	}
	var parts []string
	for off := start; off < end; {
		size, typ, _, ok := hls.ReadBoxHeader(seg, off)
		if !ok {
			break
		}
		boxEnd := off + size
		child := typ
		if typ == "mvex" || typ == "moov" || typ == "trak" {
			child += "{" + listBoxTypes(seg, off+8, boxEnd, depth+1) + "}"
		}
		parts = append(parts, child)
		off = boxEnd
	}
	return strings.Join(parts, ",")
}

func describeTopBoxes(seg []byte) string {
	var parts []string
	for off := 0; off < len(seg) && len(parts) < 6; {
		size, typ, _, ok := hls.ReadBoxHeader(seg, off)
		if !ok {
			break
		}
		parts = append(parts, typ)
		off += size
	}
	return strings.Join(parts, "+")
}
