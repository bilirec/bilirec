package mp4

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func box(typ string, payload []byte) []byte {
	size := 8 + len(payload)
	out := make([]byte, size)
	binary.BigEndian.PutUint32(out[:4], uint32(size))
	copy(out[4:8], []byte(typ))
	copy(out[8:], payload)
	return out
}

func box64(typ string, payload []byte) []byte {
	size := 16 + len(payload)
	out := make([]byte, size)
	binary.BigEndian.PutUint32(out[:4], 1)
	copy(out[4:8], []byte(typ))
	binary.BigEndian.PutUint64(out[8:16], uint64(size))
	copy(out[16:], payload)
	return out
}

func TestIsISOBMFF(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"mp4":  true,
		"MP4":  true,
		".m4v": true,
		"mov":  true,
		"mkv":  false,
		"flv":  false,
		"":     false,
	}
	for format, want := range cases {
		if got := IsISOBMFF(format); got != want {
			t.Errorf("IsISOBMFF(%q) = %v, want %v", format, got, want)
		}
	}
}

func TestHasTopLevelBox_CustomerCorpseHasNoMoov(t *testing.T) {
	t.Parallel()
	// ftyp + 8-byte free + mdat filling the rest — the ffmpeg first-pass leftover.
	data := concat(
		box("ftyp", []byte("isom")),
		box("free", nil),
		box("mdat", bytes.Repeat([]byte{0x67}, 64)),
	)
	ok, err := HasTopLevelBox(bytes.NewReader(data), int64(len(data)), boxTypeMoov)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no moov in ftyp+free+mdat corpse")
	}
}

func TestHasTopLevelBox_FtypMoov(t *testing.T) {
	t.Parallel()
	data := concat(
		box("ftyp", []byte("isom")),
		box("moov", []byte{0x00}),
	)
	ok, err := HasTopLevelBox(bytes.NewReader(data), int64(len(data)), boxTypeMoov)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected moov after ftyp")
	}
}

func TestHasTopLevelBox_FtypMdatMoov(t *testing.T) {
	t.Parallel()
	data := concat(
		box("ftyp", []byte("isom")),
		box("mdat", bytes.Repeat([]byte{0x01}, 32)),
		box("moov", []byte{0x00}),
	)
	ok, err := HasTopLevelBox(bytes.NewReader(data), int64(len(data)), boxTypeMoov)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected moov after mdat")
	}
}

func TestHasTopLevelBox_LargeSizeMoov(t *testing.T) {
	t.Parallel()
	data := concat(
		box("ftyp", []byte("isom")),
		box64("moov", []byte{0x00, 0x01}),
	)
	ok, err := HasTopLevelBox(bytes.NewReader(data), int64(len(data)), boxTypeMoov)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected 64-bit moov box")
	}
}

func TestHasTopLevelBox_SizeZeroConsumesRest(t *testing.T) {
	t.Parallel()
	ftyp := box("ftyp", []byte("isom"))
	rest := []byte("mdatXXXX")
	data := append(ftyp, rest...)
	// Rewrite the leftover as size=0 mdat (until EOF).
	copy(data[len(ftyp):], []byte{0, 0, 0, 0, 'm', 'd', 'a', 't'})
	ok, err := HasTopLevelBox(bytes.NewReader(data), int64(len(data)), boxTypeMoov)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("size-0 mdat until EOF should not report moov")
	}
}

func TestHasTopLevelBox_TruncatedBox(t *testing.T) {
	t.Parallel()
	hdr := make([]byte, 8)
	binary.BigEndian.PutUint32(hdr[:4], 1000)
	copy(hdr[4:8], []byte("mdat"))
	ok, err := HasTopLevelBox(bytes.NewReader(hdr), int64(len(hdr)), boxTypeMoov)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("truncated mdat must not report moov")
	}
}

func TestHasMoov_File(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	withMoov := filepath.Join(dir, "ok.mp4")
	without := filepath.Join(dir, "bad.mp4")
	if err := os.WriteFile(withMoov, concat(box("ftyp", []byte("isom")), box("moov", []byte{0})), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(without, concat(box("ftyp", []byte("isom")), box("mdat", []byte{0})), 0644); err != nil {
		t.Fatal(err)
	}

	ok, err := HasMoov(withMoov)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected moov")
	}
	ok, err = HasMoov(without)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("did not expect moov")
	}
}

func TestInitSize_FtypMoov(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.fmp4")
	ftyp := box("ftyp", make([]byte, 8))
	moov := box("moov", make([]byte, 16))
	media := box("moof", make([]byte, 8))
	if err := os.WriteFile(path, concat(ftyp, moov, media), 0644); err != nil {
		t.Fatal(err)
	}
	size, err := InitSize(path)
	if err != nil {
		t.Fatal(err)
	}
	want := int64(len(ftyp) + len(moov))
	if size != want {
		t.Fatalf("InitSize=%d want %d", size, want)
	}
}

func TestInitSize_Truncated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.fmp4")
	hdr := make([]byte, 8)
	binary.BigEndian.PutUint32(hdr[:4], 1000)
	copy(hdr[4:8], []byte("ftyp"))
	if err := os.WriteFile(path, hdr, 0644); err != nil {
		t.Fatal(err)
	}
	size, err := InitSize(path)
	if err != nil {
		t.Fatal(err)
	}
	if size != 0 {
		t.Fatalf("truncated InitSize=%d want 0", size)
	}
}

func concat(parts ...[]byte) []byte {
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
