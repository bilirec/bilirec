//go:build integration

package processors_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bilirec/bilirec/internal/processors"
	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/pipeline"
)

// TestFlvPipeline_TwoRotationsKeepAudioSeqHeader reproduces the production bug
// where rotation 2+ lost the AAC sequence header entirely:
//
// Upstream sends the AAC AudioSpecificConfig exactly once at stream start.
// Rotation 0→1 works because detector 0 observed it and RotationState carried
// it into FlvHeaderWriter. But detector 1 was only seeded with the video
// header, so when rotation 1→2 fired, err.AudioHeaderTag was nil — the new
// segment got no audio sequence header. flv.js then never dispatches (spinner
// forever) and ffmpeg guesses 44100 Hz (audio drift).
//
// The stream mirrors a real Bilibili HTTP-FLV session: audio seq header right
// after the first video seq header, resolution changes carry a new video
// header only, and every tag payload is unique (dedup cache must not kick in).
func TestFlvPipeline_TwoRotationsKeepAudioSeqHeader(t *testing.T) {
	avccA := avcc360p
	avccB := avcc720p
	avccC := []byte{0x01, 0x64, 0x00, 0x1f, 0xff, 0xe1, 0x00, 0x04, 0x67, 0x64, 0x00, 0x1f}

	// AudioSpecificConfig: soundFormat 10 (AAC), AACPacketType 0 (seq header).
	aacSeqPayload := []byte{0xaf, 0x00, 0x12, 0x10}

	stream := buildAVCStream(avccA, 0, true) // [preamble][vSeq1]
	// Real streams put the AAC seq header at stream start, right after the
	// video seq header. ts=0 keeps payload bytes stable across writer
	// normalization (plan review note 2).
	stream = append(stream, flv.NewTagBytes(flv.TagTypeAudio, aacSeqPayload)...)
	// Media sections must span several chunks so each rotated segment receives
	// writer output before the next rotation short-circuits the pipe.
	stream = append(stream, buildUniqueMediaTags(0, 24, 6)...)
	// Stage 1 and stage 2: mid-stream continuations with a new video header
	// (resolution change) and NO audio header — matching real upstream behavior.
	stream = append(stream, buildAVCStream(avccB, 0, false)...)
	stream = append(stream, buildUniqueMediaTags(1, 24, 6)...)
	stream = append(stream, buildAVCStream(avccC, 0, false)...)
	stream = append(stream, buildUniqueMediaTags(2, 24, 6)...)

	tmpDir := t.TempDir()
	outFile := func(seg int) string {
		return filepath.Join(tmpDir, "seg"+string(rune('0'+seg))+".flv")
	}
	sharedFixer := flv.NewRealtimeFixer()
	defer sharedFixer.Close()

	openPipe := func(videoHdr, audioHdr []byte, outPath string) *pipeline.Pipe[[]byte] {
		var splitDetector *pipeline.ProcessorInfo[[]byte]
		if len(videoHdr) > 0 {
			splitDetector = processors.NewFlvHeaderSplitDetectorSeeded(videoHdr, audioHdr)
		} else {
			splitDetector = processors.NewFlvHeaderSplitDetector()
		}
		p := pipeline.New(
			processors.NewFlvStreamFixerWithFixer(sharedFixer),
			splitDetector,
			processors.NewFlvHeaderWriter(videoHdr, audioHdr),
			processors.NewBufferedStreamWriter(outPath,
				processors.WithBufferSize(4*1024*1024),
				processors.WithSkipSmallFlushThreshold(0),
			),
		)
		if err := p.Open(context.Background()); err != nil {
			t.Fatalf("open pipeline: %v", err)
		}
		return p
	}

	const chunkSize = 512
	segment := 0
	rotations := 0
	var videoHdr, audioHdr []byte
	var audioHdrByRotation [][]byte
	pipe := openPipe(nil, nil, outFile(segment))

	for offset := 0; offset < len(stream); {
		end := offset + chunkSize
		if end > len(stream) {
			end = len(stream)
		}
		_, procErr := pipe.Process(context.Background(), stream[offset:end])
		offset = end

		if procErr != nil {
			var headerChanged *flv.FlvHeaderChangedError
			if errors.As(procErr, &headerChanged) {
				rotations++
				t.Logf("rotation %d: videoHdr=%dB audioHdr=%dB",
					rotations, len(headerChanged.VideoHeaderTag), len(headerChanged.AudioHeaderTag))
				pipe.Close()
				videoHdr = headerChanged.VideoHeaderTag
				audioHdr = headerChanged.AudioHeaderTag
				audioHdrByRotation = append(audioHdrByRotation, audioHdr)
				sharedFixer.ResetTimestampStore()
				sharedFixer.ResetDedupCache()
				segment++
				pipe = openPipe(videoHdr, audioHdr, outFile(segment))
				continue
			}
			t.Fatalf("unexpected pipeline error at offset %d: %v", offset, procErr)
		}
	}
	pipe.Close()

	if rotations != 2 {
		t.Fatalf("expected exactly 2 rotations, got %d", rotations)
	}

	// Rotation 1 (0→1): audio header came from detector 0 observing stage 0's
	// own audio seq header — existing behavior, must keep working.
	if len(audioHdrByRotation[0]) == 0 {
		t.Fatal("rotation 1: AudioHeaderTag must not be empty (stage 0 carried the audio seq header)")
	}
	// Rotation 2 (1→2): the fix under test. Detector 1 was seeded with the
	// audio header, so err.AudioHeaderTag must be non-empty. Compare payloads
	// (tag data only) to avoid timestamp-byte differences between capture and
	// seed.
	rot1Data := audioHdrByRotation[0][flv.TagHeaderSize : len(audioHdrByRotation[0])-flv.PrevTagSizeBytes]
	rot2Data := audioHdrByRotation[1][flv.TagHeaderSize : len(audioHdrByRotation[1])-flv.PrevTagSizeBytes]
	if !bytes.Equal(rot1Data, rot2Data) {
		t.Fatalf("rotation 2: audio header payload changed: %x, want %x", rot2Data, rot1Data)
	}

	// Every output segment must start with [video seq tag][audio seq tag]:
	//   - seg0 because the upstream stream is constructed that way,
	//   - seg1/seg2 because FlvHeaderWriter injects both headers.
	for seg := 0; seg <= 2; seg++ {
		data, err := os.ReadFile(outFile(seg))
		if err != nil {
			t.Fatalf("read seg%d: %v", seg, err)
		}
		if len(data) < flv.FlvHeaderSize+flv.PrevTagSizeBytes {
			t.Fatalf("seg%d too small: %d bytes", seg, len(data))
		}
		rest := data[flv.FlvHeaderSize+flv.PrevTagSizeBytes:]

		// First tag: video sequence header.
		vTag, rest2, ok, skip := flv.NextTag(rest)
		if skip || !ok {
			t.Fatalf("seg%d: cannot parse first tag", seg)
		}
		kind, isHeader, _ := flv.ClassifyVideoTag(flv.TagData(vTag))
		if !isHeader || kind == flv.VideoKindNone {
			t.Fatalf("seg%d: first tag is not a video sequence header", seg)
		}

		// Second tag: AAC sequence header (af 00 ...).
		aTag, _, ok2, skip2 := flv.NextTag(rest2)
		if skip2 || !ok2 {
			t.Fatalf("seg%d: cannot parse second tag", seg)
		}
		isAAC, isSeq := flv.ClassifyAudioTag(flv.TagData(aTag))
		if !isAAC || !isSeq {
			t.Fatalf("seg%d: second tag is not an AAC sequence header (got %x...)", seg, flv.TagData(aTag)[:2])
		}
		t.Logf("seg%d.flv: %d bytes, [video hdr][AAC seq hdr] at top", seg, len(data))
	}
}

// buildUniqueMediaTags builds numInter inter-frame video tags and numAudio raw
// AAC frame tags whose payloads embed the stream index, so the dedup cache
// (which hashes the first 32 payload bytes) never treats them as duplicates —
// mirroring real streams where every frame differs.
func buildUniqueMediaTags(stageIndex, numInter, numAudio int) []byte {
	var b []byte
	for i := 0; i < numInter; i++ {
		payload := make([]byte, 6+50)
		payload[0] = 0x27 // FrameType=2 (inter) | CodecID=7 (AVC)
		payload[1] = 0x01 // AVCPacketType=1 (NALU)
		// Embed a unique signature early (within the 32-byte dedup window).
		payload[5] = byte(stageIndex)
		payload[6] = byte(i)
		b = append(b, flv.NewTagBytes(flv.TagTypeVideo, payload)...)
	}
	for i := 0; i < numAudio; i++ {
		payload := []byte{0xaf, 0x01, byte(stageIndex), byte(i), 0x55}
		b = append(b, flv.NewTagBytes(flv.TagTypeAudio, payload)...)
	}
	return b
}
