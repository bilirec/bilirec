package convert

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/bilirec/bilirec/pkg/ffmpeg"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/utils"
)

func isoBox(typ string, payload []byte) []byte {
	size := 8 + len(payload)
	out := make([]byte, size)
	binary.BigEndian.PutUint32(out[:4], uint32(size))
	copy(out[4:8], []byte(typ))
	copy(out[8:], payload)
	return out
}

func concatBoxes(parts ...[]byte) []byte {
	var n int
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// writePaddedISOBMFF writes a top-level ISO BMFF file of exactly total bytes.
func writePaddedISOBMFF(t *testing.T, path string, withMoov bool, total int64) {
	t.Helper()
	head := isoBox("ftyp", []byte("isomiso2mp41"))
	if withMoov {
		head = concatBoxes(head, isoBox("moov", []byte{0x00}))
	} else {
		head = concatBoxes(head, isoBox("free", nil))
	}
	remain := total - int64(len(head)) - 8
	if remain < 0 {
		t.Fatalf("total %d too small for headers", total)
	}
	mdat := make([]byte, 8+remain)
	binary.BigEndian.PutUint32(mdat[:4], uint32(len(mdat)))
	copy(mdat[4:8], "mdat")
	if err := os.WriteFile(path, append(head, mdat...), 0644); err != nil {
		t.Fatal(err)
	}
}

func writeDummy(t *testing.T, path string, size int64) {
	t.Helper()
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x11}, int(size)), 0644); err != nil {
		t.Fatal(err)
	}
}

func skipIfProbeAvailable(t *testing.T) {
	t.Helper()
	if ffmpeg.ProbeAvailable() {
		t.Skip("synthetic ISO BMFF is only accepted by the moov fallback when ffprobe is unavailable")
	}
}

func TestValidateOutputFileSize_TooSmall(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "in.flv")
	out := filepath.Join(dir, "out.mp4")
	writeDummy(t, input, minimumExportedFileBytesRequired)
	writeDummy(t, out, minimumExportedFileBytesRequired-1)
	if err := validateOutputFileSize(input, out); err == nil {
		t.Fatal("expected size failure")
	}
}

func TestValidateOutputContainer_Invalid(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.mp4")
	writePaddedISOBMFF(t, out, false, 64)
	if err := validateOutputContainer(context.Background(), logger.Nop(), out, "mp4"); err == nil {
		t.Fatal("expected invalid container")
	}
}

func TestValidateOutputContainer_MoovFallback(t *testing.T) {
	skipIfProbeAvailable(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "out.mp4")
	writePaddedISOBMFF(t, out, true, 64)
	if err := validateOutputContainer(context.Background(), logger.Nop(), out, "mp4"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateOutputContainer_StagingTmp(t *testing.T) {
	skipIfProbeAvailable(t)
	dir := t.TempDir()
	staging := utils.StagingPath(filepath.Join(dir, "out.mp4"))
	writePaddedISOBMFF(t, staging, true, 64)
	if err := validateOutputContainer(context.Background(), logger.Nop(), staging, "mp4"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateOutputContainer_NoProbeNonISOBMFF(t *testing.T) {
	skipIfProbeAvailable(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "out.mkv")
	writeDummy(t, out, 64)
	if err := validateOutputContainer(context.Background(), logger.Nop(), out, "mkv"); err == nil {
		t.Fatal("expected failure when ffprobe is missing and format is not ISO-BMFF")
	}
}

func TestProcessTask_SkipValidExistingOutput(t *testing.T) {
	skipIfProbeAvailable(t)
	dir := t.TempDir()
	input := filepath.Join(dir, "in.flv")
	out := filepath.Join(dir, "out.mp4")
	writeDummy(t, input, 64)
	writePaddedISOBMFF(t, out, true, 64)

	var mgr ffmpegConvertManager
	if err := mgr.processTask(context.Background(), &TaskQueue{
		InputPath:    input,
		OutputPath:   out,
		OutputFormat: "mp4",
	}, logger.Nop()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(utils.StagingPath(out)); !os.IsNotExist(err) {
		t.Fatal("skip path must not leave a staging file")
	}
}

func TestProcessTask_InvalidExistingOutputRetriesAndCleansTmp(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "in.flv")
	out := filepath.Join(dir, "out.mp4")
	writeDummy(t, input, 64)
	writePaddedISOBMFF(t, out, false, 64)

	var mgr ffmpegConvertManager
	err := mgr.processTask(context.Background(), &TaskQueue{
		InputPath:    input,
		OutputPath:   out,
		OutputFormat: "mp4",
	}, logger.Nop())
	if err == nil {
		t.Fatal("expected ffmpeg failure on dummy input")
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal("invalid final output is replaced only after a successful convert")
	}
	if _, statErr := os.Stat(utils.StagingPath(out)); !os.IsNotExist(statErr) {
		t.Fatalf("staging file should be cleaned after failure, stat err=%v", statErr)
	}
}

func TestIsConvertedFileInvalid(t *testing.T) {
	tests := []struct {
		name       string
		downloaded int64
		input      int64
		want       bool
	}{
		{
			name:       "invalid when output less than 1MB",
			downloaded: minimumExportedFileBytesRequired - 1,
			input:      10 * 1024 * 1024,
			want:       true,
		},
		{
			name:       "invalid when output less than half of input",
			downloaded: 3 * 1024 * 1024,
			input:      7 * 1024 * 1024,
			want:       true,
		},
		{
			name:       "valid at exact 1MB and half of input",
			downloaded: minimumExportedFileBytesRequired,
			input:      2 * minimumExportedFileBytesRequired,
			want:       false,
		},
		{
			name:       "valid when source size unavailable and output above 1MB",
			downloaded: 2 * minimumExportedFileBytesRequired,
			input:      0,
			want:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isConvertedFileInvalid(tc.downloaded, tc.input)
			if got != tc.want {
				t.Fatalf("isConvertedFileInvalid(%d, %d) = %v, want %v", tc.downloaded, tc.input, got, tc.want)
			}
		})
	}
}
