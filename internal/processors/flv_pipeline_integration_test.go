package processors_test

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/bilirec/bilirec/internal/processors"
	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/pipeline"
)

// buildAVCStream constructs a minimal synthetic FLV stream containing:
//   - FLV file preamble (if withPreamble=true)
//   - One AVC/H.264 sequence header tag with the given AVCC bytes
//   - numDataTags dummy inter-frame video tags
//
// The resulting bytes can be appended to form a mid-stream continuation by
// setting withPreamble=false (no FLV file header, tags follow directly).
func buildAVCStream(avccBytes []byte, numDataTags int, withPreamble bool) []byte {
	var b []byte
	if withPreamble {
		b = append(b, flv.NewFileHeaderBytes()...)
	}

	// AVC sequence header tag:
	//   tagData[0] = 0x17: FrameType=1 (keyframe) | CodecID=7 (AVC)
	//   tagData[1] = 0x00: AVCPacketType=0 (sequence header)
	//   tagData[2..4] = composition time offset = 0
	//   tagData[5..] = AVCDecoderConfigurationRecord (avccBytes)
	seqData := make([]byte, 0, 5+len(avccBytes))
	seqData = append(seqData, 0x17, 0x00, 0x00, 0x00, 0x00)
	seqData = append(seqData, avccBytes...)
	b = append(b, flv.NewTagBytes(flv.TagTypeVideo, seqData)...)

	// Dummy inter-frame video tags (not seq headers, won't trigger rotation)
	frame := make([]byte, 6+50)
	frame[0] = 0x27 // FrameType=2 (inter) | CodecID=7 (AVC)
	frame[1] = 0x01 // AVCPacketType=1 (NALU)
	for i := 0; i < numDataTags; i++ {
		b = append(b, flv.NewTagBytes(flv.TagTypeVideo, frame)...)
	}
	return b
}

// avcc360p and avcc720p are minimal fake AVCDecoderConfigurationRecords that
// differ in profile/level bytes, ensuring the detector sees a header change.
var (
	avcc360p = []byte{0x01, 0x42, 0x00, 0x1f, 0xff, 0xe1, 0x00, 0x04, 0x67, 0x42, 0x00, 0x1f}
	avcc720p = []byte{0x01, 0x64, 0x00, 0x29, 0xff, 0xe1, 0x00, 0x04, 0x67, 0x64, 0x00, 0x29}
)

func buildContinuousHTTPFLVStreamFromFixtures(fixtureNames []string) ([]byte, [][]byte, error) {
	fixtures := make([][]byte, 0, len(fixtureNames))
	for _, name := range fixtureNames {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			return nil, nil, err
		}
		fixtures = append(fixtures, data)
	}

	preamble := flv.FlvHeaderSize + flv.PrevTagSizeBytes
	for i, data := range fixtures {
		if len(data) <= preamble {
			return nil, nil, errors.New("fixture too small: " + fixtureNames[i])
		}
	}

	stream := make([]byte, 0, len(fixtures[0]))
	stream = append(stream, fixtures[0]...)
	lastTs := maxTagTimestamp(fixtures[0][preamble:])

	for i := 1; i < len(fixtures); i++ {
		var err error
		stream, err = appendNormalizedContinuation(stream, fixtures[i][preamble:], &lastTs)
		if err != nil {
			return nil, nil, err
		}
	}

	return stream, fixtures, nil
}

func maxTagTimestamp(payload []byte) int64 {
	var maxTs int64
	offset := 0
	for offset+flv.TagHeaderSize <= len(payload) {
		dataSize := int(payload[offset+1])<<16 | int(payload[offset+2])<<8 | int(payload[offset+3])
		tagEnd := offset + flv.TagHeaderSize + dataSize
		fullEnd := tagEnd + flv.PrevTagSizeBytes
		if fullEnd > len(payload) {
			break
		}
		ts := int64(uint32(payload[offset+7])<<24 | uint32(payload[offset+4])<<16 | uint32(payload[offset+5])<<8 | uint32(payload[offset+6]))
		if ts > maxTs {
			maxTs = ts
		}
		offset = fullEnd
	}
	return maxTs
}

func appendNormalizedContinuation(dst []byte, payload []byte, lastTs *int64) ([]byte, error) {
	minTs := int64(-1)
	offset := 0
	for offset+flv.TagHeaderSize <= len(payload) {
		tagType := payload[offset]
		dataSize := int(payload[offset+1])<<16 | int(payload[offset+2])<<8 | int(payload[offset+3])
		tagEnd := offset + flv.TagHeaderSize + dataSize
		fullEnd := tagEnd + flv.PrevTagSizeBytes
		if fullEnd > len(payload) {
			break
		}
		if tagType != flv.TagTypeScript {
			ts := int64(uint32(payload[offset+7])<<24 | uint32(payload[offset+4])<<16 | uint32(payload[offset+5])<<8 | uint32(payload[offset+6]))
			if minTs < 0 || ts < minTs {
				minTs = ts
			}
		}
		offset = fullEnd
	}

	delta := int64(0)
	if minTs >= 0 && minTs <= *lastTs {
		delta = (*lastTs + 1) - minTs
	}

	offset = 0
	for offset+flv.TagHeaderSize <= len(payload) {
		tagType := payload[offset]
		dataSize := int(payload[offset+1])<<16 | int(payload[offset+2])<<8 | int(payload[offset+3])
		tagEnd := offset + flv.TagHeaderSize + dataSize
		fullEnd := tagEnd + flv.PrevTagSizeBytes
		if fullEnd > len(payload) {
			break
		}
		if tagType != flv.TagTypeScript {
			ts := int64(uint32(payload[offset+7])<<24|uint32(payload[offset+4])<<16|uint32(payload[offset+5])<<8|uint32(payload[offset+6])) + delta
			if ts < 0 {
				ts = 0
			}
			if ts > 0xFFFFFFFF {
				ts = 0xFFFFFFFF
			}

			head := make([]byte, flv.TagHeaderSize)
			copy(head, payload[offset:offset+flv.TagHeaderSize])
			head[4] = byte(ts >> 16)
			head[5] = byte(ts >> 8)
			head[6] = byte(ts)
			head[7] = byte(ts >> 24)

			dst = append(dst, head...)
			dst = append(dst, payload[offset+flv.TagHeaderSize:tagEnd]...)

			prev := make([]byte, 4)
			binary.BigEndian.PutUint32(prev, uint32(flv.TagHeaderSize+dataSize))
			dst = append(dst, prev...)

			if ts > *lastTs {
				*lastTs = ts
			}
		}
		offset = fullEnd
	}

	return dst, nil
}

// TestFlvPipeline_ResolutionChangeRotation feeds a synthetic HTTP-FLV stream
// that switches from 360p to 720p AVC parameters mid-stream through the full
// processor pipeline and verifies that exactly one rotation occurs and that
// both output segments begin with a valid FLV file header.
func TestFlvPipeline_ResolutionChangeRotation(t *testing.T) {
	// Build a simulated HTTP-FLV stream:
	//   [full 360p FLV file] + [720p continuation without FLV preamble]
	stream360 := buildAVCStream(avcc360p, 20, true)  // with FLV header
	stream720 := buildAVCStream(avcc720p, 20, false) // mid-stream, no header
	stream := append(stream360, stream720...)

	tmpDir := t.TempDir()
	outFile := func(seg int) string {
		return filepath.Join(tmpDir, "seg"+string(rune('0'+seg))+".flv")
	}
	sharedFixer := flv.NewRealtimeFixer()
	defer sharedFixer.Close()

	openPipe := func(videoHdr, audioHdr []byte, outPath string) *pipeline.Pipe[[]byte] {
		p := pipeline.New(
			processors.NewFlvStreamFixerWithFixer(sharedFixer),
			processors.NewFlvHeaderSplitDetector(),
			processors.NewFlvHeaderWriter(videoHdr, audioHdr),
			processors.NewBufferedStreamWriter(outPath, processors.WithBufferSize(4*1024*1024)),
		)
		if err := p.Open(context.Background()); err != nil {
			t.Fatalf("open pipeline: %v", err)
		}
		return p
	}

	const chunkSize = 512 // small chunks to stress-test buffer boundaries
	segment := 0
	var videoHdr, audioHdr []byte
	pipe := openPipe(nil, nil, outFile(segment))
	rotations := 0

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
				t.Logf("rotation %d triggered at stream offset %d", rotations+1, offset)
				pipe.Close()
				videoHdr = headerChanged.VideoHeaderTag
				audioHdr = headerChanged.AudioHeaderTag
				sharedFixer.ResetTimestampStore()
				sharedFixer.ResetDedupCache()
				rotations++
				segment++
				pipe = openPipe(videoHdr, audioHdr, outFile(segment))
				continue
			}
			t.Fatalf("unexpected pipeline error at offset %d: %v", offset, procErr)
		}
	}
	pipe.Close()

	if rotations != 1 {
		t.Fatalf("expected exactly 1 rotation, got %d", rotations)
	}

	// Verify both output segments: non-trivial size and valid FLV magic.
	for seg := 0; seg <= 1; seg++ {
		path := outFile(seg)
		st, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat seg%d: %v", seg, err)
		}
		if st.Size() < int64(flv.FlvHeaderSize+flv.PrevTagSizeBytes) {
			t.Fatalf("seg%d.flv too small: %d bytes", seg, st.Size())
		}
		header := make([]byte, 3)
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("open seg%d: %v", seg, err)
		}
		_, err = io.ReadFull(f, header)
		f.Close()
		if err != nil {
			t.Fatalf("read seg%d header: %v", seg, err)
		}
		if header[0] != 'F' || header[1] != 'L' || header[2] != 'V' {
			t.Fatalf("seg%d.flv does not start with FLV magic, got %v", seg, header)
		}
		t.Logf("seg%d.flv: %d bytes, valid FLV header ✓", seg, st.Size())
	}

	// seg1 must carry the 720p AVC sequence header injected by FlvHeaderWriter.
	if len(videoHdr) == 0 {
		t.Fatal("expected non-empty VideoHeaderTag after rotation")
	}
	t.Logf("VideoHeaderTag: %d bytes, AudioHeaderTag: %d bytes", len(videoHdr), len(audioHdr))
}

// TestFlvPipeline_ResolutionChangeRotation_RealFixtures uses local real-world
// fixtures in testdata/ to simulate an HTTP-FLV mid-stream resolution change:
//
//	[A.flv full stream] + [B.flv without FLV preamble].
//
// This test is intentionally optional: if fixtures are missing in CI, it skips.
func TestFlvPipeline_ResolutionChangeRotation_RealFixtures(t *testing.T) {
	fixtureNames := []string{"A.flv", "B.flv", "C.flv", "D.flv"}
	stream, _, err := buildContinuousHTTPFLVStreamFromFixtures(fixtureNames)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("real fixtures not found: %v", err)
		}
		t.Fatalf("build continuous stream from fixtures: %v", err)
	}

	tmpDir := t.TempDir()
	outFile := func(seg int) string {
		return filepath.Join(tmpDir, "real-seg"+string(rune('0'+seg))+".flv")
	}
	sharedFixer := flv.NewRealtimeFixer()
	defer sharedFixer.Close()

	openPipe := func(videoHdr, audioHdr []byte, outPath string) *pipeline.Pipe[[]byte] {
		var splitDetector *pipeline.ProcessorInfo[[]byte]
		if len(videoHdr) > 0 {
			splitDetector = processors.NewFlvHeaderSplitDetectorSeeded(videoHdr)
		} else {
			splitDetector = processors.NewFlvHeaderSplitDetector()
		}
		p := pipeline.New(
			processors.NewFlvStreamFixerWithFixer(sharedFixer),
			splitDetector,
			processors.NewFlvHeaderWriter(videoHdr, audioHdr),
			processors.NewBufferedStreamWriter(outPath, processors.WithBufferSize(4*1024*1024)),
		)
		if err := p.Open(context.Background()); err != nil {
			t.Fatalf("open pipeline: %v", err)
		}
		return p
	}

	const chunkSize = 32 * 1024
	segment := 0
	rotations := 0
	var videoHdr, audioHdr []byte
	var pendingData []byte
	pipe := openPipe(nil, nil, outFile(segment))

	for offset := 0; offset < len(stream); {
		if len(pendingData) > 0 {
			if _, perr := pipe.Process(context.Background(), pendingData); perr != nil {
				t.Fatalf("failed to replay pending split chunk: %v", perr)
			}
			pendingData = nil
		}

		end := offset + chunkSize
		if end > len(stream) {
			end = len(stream)
		}

		_, procErr := pipe.Process(context.Background(), stream[offset:end])
		offset = end

		if procErr != nil {
			var headerChanged *flv.FlvHeaderChangedError
			if errors.As(procErr, &headerChanged) {
				t.Logf("real fixture rotation %d at stream offset %d", rotations+1, offset)
				pipe.Close()
				videoHdr = headerChanged.VideoHeaderTag
				audioHdr = headerChanged.AudioHeaderTag
				sharedFixer.ResetTimestampStore()
				sharedFixer.ResetDedupCache()
				rotations++
				segment++
				pipe = openPipe(videoHdr, audioHdr, outFile(segment))
				continue
			}
			t.Fatalf("unexpected pipeline error at offset %d: %v", offset, procErr)
		}
	}
	pipe.Close()

	if rotations < 1 {
		t.Fatalf("expected at least 1 rotation from real fixtures, got %d", rotations)
	}

	for seg := 0; seg <= segment; seg++ {
		path := outFile(seg)
		st, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("stat real-seg%d: %v", seg, statErr)
		}
		if st.Size() < int64(flv.FlvHeaderSize+flv.PrevTagSizeBytes) {
			t.Fatalf("real-seg%d.flv too small: %d bytes", seg, st.Size())
		}

		header := make([]byte, 3)
		f, openErr := os.Open(path)
		if openErr != nil {
			t.Fatalf("open real-seg%d: %v", seg, openErr)
		}
		_, readErr := io.ReadFull(f, header)
		f.Close()
		if readErr != nil {
			t.Fatalf("read real-seg%d header: %v", seg, readErr)
		}
		if header[0] != 'F' || header[1] != 'L' || header[2] != 'V' {
			t.Fatalf("real-seg%d.flv missing FLV magic: %v", seg, header)
		}
	}

	if len(videoHdr) == 0 {
		t.Fatal("expected non-empty VideoHeaderTag after real-fixture rotation")
	}
}

// TestFlvPipeline_GenerateBeforeAfterArtifacts writes concrete FLV artifacts
// for manual visual comparison (Before/After) using real fixtures.
//
// Enable by setting BILIREC_WRITE_SIM_ARTIFACTS=1.
// Output directory: internal/processors/testdata/simulated
func TestFlvPipeline_GenerateBeforeAfterArtifacts(t *testing.T) {
	if os.Getenv("BILIREC_WRITE_SIM_ARTIFACTS") != "1" {
		t.Skip("set BILIREC_WRITE_SIM_ARTIFACTS=1 to generate before/after FLV artifacts")
	}

	fixtureNames := []string{"A.flv", "B.flv", "C.flv", "D.flv"}
	mixed, fixtures, err := buildContinuousHTTPFLVStreamFromFixtures(fixtureNames)
	if err != nil {
		t.Fatalf("build continuous stream from fixtures: %v", err)
	}
	preamble := flv.FlvHeaderSize + flv.PrevTagSizeBytes

	outDir := filepath.Join("testdata", "simulated")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatalf("mkdir simulated output: %v", err)
	}

	// 1) headless continuation chunk (no FLV file header)
	headless := fixtures[1][preamble:]
	headlessPath := filepath.Join(outDir, "before_headless_continuation.flv")
	if err := os.WriteFile(headlessPath, headless, 0644); err != nil {
		t.Fatalf("write headless artifact: %v", err)
	}

	// 2) single mixed file without split (commonly leads to mosaic/artifacts)
	// built from normalized A+B+C+D continuation stream.
	mixedPath := filepath.Join(outDir, "before_mixed_no_split.flv")
	if err := os.WriteFile(mixedPath, mixed, 0644); err != nil {
		t.Fatalf("write mixed artifact: %v", err)
	}

	// 2.5) bilibili-like HTTP-FLV artifact from the COMBINED stream:
	// keep FLV preamble, but remove script metadata tags so duration is not fixed.
	filterOutScriptTags := func(payload []byte) []byte {
		out := make([]byte, 0, len(payload))
		offset := 0
		for offset+flv.TagHeaderSize <= len(payload) {
			tagType := payload[offset]
			dataSize := int(payload[offset+1])<<16 | int(payload[offset+2])<<8 | int(payload[offset+3])
			tagEnd := offset + flv.TagHeaderSize + dataSize
			fullEnd := tagEnd + flv.PrevTagSizeBytes
			if fullEnd > len(payload) {
				out = append(out, payload[offset:]...)
				break
			}
			if tagType != flv.TagTypeScript {
				out = append(out, payload[offset:fullEnd]...)
			}
			offset = fullEnd
		}
		if offset < len(payload) {
			out = append(out, payload[offset:]...)
		}
		return out
	}
	httpFlvPayload := filterOutScriptTags(mixed[preamble:])
	httpFlvLike := make([]byte, 0, preamble+len(httpFlvPayload))
	httpFlvLike = append(httpFlvLike, flv.NewFileHeaderBytes()...)
	httpFlvLike = append(httpFlvLike, httpFlvPayload...)
	httpFlvLikePath := filepath.Join(outDir, "before_httpflv_like_no_duration.flv")
	if err := os.WriteFile(httpFlvLikePath, httpFlvLike, 0644); err != nil {
		t.Fatalf("write http-flv-like artifact: %v", err)
	}
	if len(httpFlvLike) <= len(fixtures[0]) {
		t.Fatalf("http-flv-like artifact is not combined A+B+C+D: got %d, A=%d", len(httpFlvLike), len(fixtures[0]))
	}
	if len(httpFlvLike) >= len(mixed) {
		t.Fatalf("http-flv-like artifact did not remove metadata as expected: http=%d mixed=%d", len(httpFlvLike), len(mixed))
	}
	firstTagType := httpFlvLike[preamble]
	if firstTagType == flv.TagTypeScript {
		t.Fatalf("http-flv-like artifact still starts with script metadata tag")
	}

	// 3) split by pipeline into after_segment_*.flv
	outFile := func(seg int) string {
		return filepath.Join(outDir, "after_segment_"+string(rune('0'+seg))+".flv")
	}
	sharedFixer := flv.NewRealtimeFixer()
	defer sharedFixer.Close()

	openPipe := func(videoHdr, audioHdr []byte, outPath string) *pipeline.Pipe[[]byte] {
		var splitDetector *pipeline.ProcessorInfo[[]byte]
		if len(videoHdr) > 0 {
			splitDetector = processors.NewFlvHeaderSplitDetectorSeeded(videoHdr)
		} else {
			splitDetector = processors.NewFlvHeaderSplitDetector()
		}
		p := pipeline.New(
			processors.NewFlvStreamFixerWithFixer(sharedFixer),
			splitDetector,
			processors.NewFlvHeaderWriter(videoHdr, audioHdr),
			processors.NewBufferedStreamWriter(outPath, processors.WithBufferSize(4*1024*1024)),
		)
		if openErr := p.Open(context.Background()); openErr != nil {
			t.Fatalf("open pipeline: %v", openErr)
		}
		return p
	}

	segment := 0
	rotations := 0
	var videoHdr, audioHdr []byte
	pipe := openPipe(nil, nil, outFile(segment))
	const chunkSize = 32 * 1024

	for offset := 0; offset < len(mixed); {
		end := offset + chunkSize
		if end > len(mixed) {
			end = len(mixed)
		}

		_, procErr := pipe.Process(context.Background(), mixed[offset:end])
		offset = end

		if procErr != nil {
			var headerChanged *flv.FlvHeaderChangedError
			if errors.As(procErr, &headerChanged) {
				pipe.Close()
				videoHdr = headerChanged.VideoHeaderTag
				audioHdr = headerChanged.AudioHeaderTag
				// Reset fixer's timestamp state for new segment so timestamps start from 0
				sharedFixer.ResetTimestampStore()
				sharedFixer.ResetDedupCache()
				rotations++
				segment++
				pipe = openPipe(videoHdr, audioHdr, outFile(segment))
				continue
			}
			t.Fatalf("unexpected pipeline error at offset %d: %v", offset, procErr)
		}
	}
	pipe.Close()

	// 4) realtime-fixer only pipeline (NO header split detector):
	// this simulates old behavior where stream continues across resolution change
	// in a single output file.
	noSplitPath := filepath.Join(outDir, "before_realtime_fixer_no_split.flv")
	noSplitPipe := pipeline.New(
		processors.NewFlvStreamFixer(),
		processors.NewFlvHeaderWriter(nil, nil),
		processors.NewBufferedStreamWriter(noSplitPath, processors.WithBufferSize(4*1024*1024)),
	)
	if openErr := noSplitPipe.Open(context.Background()); openErr != nil {
		t.Fatalf("open no-split pipeline: %v", openErr)
	}
	for offset := 0; offset < len(mixed); {
		end := offset + chunkSize
		if end > len(mixed) {
			end = len(mixed)
		}
		if _, procErr := noSplitPipe.Process(context.Background(), mixed[offset:end]); procErr != nil {
			noSplitPipe.Close()
			t.Fatalf("no-split pipeline error at offset %d: %v", offset, procErr)
		}
		offset = end
	}
	noSplitPipe.Close()

	if st, statErr := os.Stat(noSplitPath); statErr != nil {
		t.Fatalf("stat no-split artifact: %v", statErr)
	} else if st.Size() <= int64(flv.FlvHeaderSize+flv.PrevTagSizeBytes) {
		t.Fatalf("no-split artifact too small: %d", st.Size())
	}

	t.Logf("generated artifacts at %s", outDir)
	t.Logf("headless: %s", headlessPath)
	t.Logf("http-flv-like no-duration: %s", httpFlvLikePath)
	t.Logf("before mixed (no split): %s", mixedPath)
	t.Logf("before realtime-fixer no split: %s", noSplitPath)
	for i := 0; i <= segment; i++ {
		t.Logf("after segment %d: %s", i, outFile(i))
	}
	t.Logf("pipeline rotations: %d", rotations)
}

// TestFlvPipeline_RealtimeFixerNoSplit_RealFixtures simulates pipeline behavior
// when using realtime fixer but NOT using the header split detector.
func TestFlvPipeline_RealtimeFixerNoSplit_RealFixtures(t *testing.T) {
	fixtureNames := []string{"A.flv", "B.flv", "C.flv", "D.flv"}
	stream, _, err := buildContinuousHTTPFLVStreamFromFixtures(fixtureNames)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("real fixtures not found: %v", err)
		}
		t.Fatalf("build continuous stream from fixtures: %v", err)
	}

	outPath := filepath.Join(t.TempDir(), "realtime_fixer_no_split.flv")
	pipe := pipeline.New(
		processors.NewFlvStreamFixer(),
		processors.NewFlvHeaderWriter(nil, nil),
		processors.NewBufferedStreamWriter(outPath, processors.WithBufferSize(4*1024*1024)),
	)
	if err := pipe.Open(context.Background()); err != nil {
		t.Fatalf("open pipeline: %v", err)
	}

	const chunkSize = 32 * 1024
	for offset := 0; offset < len(stream); {
		end := offset + chunkSize
		if end > len(stream) {
			end = len(stream)
		}
		if _, err := pipe.Process(context.Background(), stream[offset:end]); err != nil {
			pipe.Close()
			t.Fatalf("no-split process failed at offset %d: %v", offset, err)
		}
		offset = end
	}
	pipe.Close()

	header := make([]byte, 3)
	f, err := os.Open(outPath)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	_, err = io.ReadFull(f, header)
	f.Close()
	if err != nil {
		t.Fatalf("read output header: %v", err)
	}
	if header[0] != 'F' || header[1] != 'L' || header[2] != 'V' {
		t.Fatalf("no-split output missing FLV magic: %v", header)
	}
}
