package preview

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/bilirec/bilirec/pkg/flv"
)

func TestFLVLiveJoinStartsAtRecentKeyframe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.bin")
	seq := flv.NewTagBytes(flv.TagTypeVideo, []byte{0x17, 0x00, 0x00, 0x00, 0x00, 0x01})
	audio := flv.NewTagBytes(flv.TagTypeAudio, []byte{0xaf, 0x00, 0x12, 0x10})
	inter := flv.NewTagBytes(flv.TagTypeVideo, []byte{0x27, 0x01, 0x00, 0x00, 0x00, 'E'})
	key := flv.NewTagBytes(flv.TagTypeVideo, []byte{0x17, 0x01, 0x00, 0x00, 0x00, 'K'})
	setTagTS(inter, 1_000)
	setTagTS(key, 60_000)

	var file bytes.Buffer
	file.Write(flv.NewFileHeaderBytes())
	file.Write(seq)
	file.Write(audio)
	for i := 0; i < 40; i++ {
		file.Write(inter)
	}
	keyOff := int64(file.Len())
	file.Write(key)
	file.Write(inter)
	if err := os.WriteFile(path, file.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	join, ok := flvLiveJoin(path)
	if !ok {
		t.Fatal("expected a late-join prefix")
	}
	if !bytes.HasPrefix(join.prefix, flv.NewFileHeaderBytes()) {
		t.Fatal("prefix missing FLV file header")
	}
	if !bytes.Contains(join.prefix, seq) {
		t.Fatal("prefix missing video sequence header")
	}
	if !bytes.Contains(join.prefix, audio) {
		t.Fatal("prefix missing audio sequence header")
	}
	if bytes.Contains(join.prefix, []byte{'E'}) {
		t.Fatal("prefix must not include early inter frames")
	}
	if join.offset != keyOff {
		t.Fatalf("join offset=%d want keyframe at %d", join.offset, keyOff)
	}
	if join.baseTS != 60_000 {
		t.Fatalf("baseTS=%d want 60000", join.baseTS)
	}
}

func TestRebaseFLVTimestampsFromJoin(t *testing.T) {
	key := flv.NewTagBytes(flv.TagTypeVideo, []byte{0x17, 0x01, 0x00, 0x00, 0x00, 'K'})
	setTagTS(key, 60_000)
	var out bytes.Buffer
	r := &tsRebaser{dst: &out, base: 60_000}
	if _, err := r.Write(key); err != nil {
		t.Fatal(err)
	}
	if got := flvTagTS(out.Bytes()); got != 0 {
		t.Fatalf("rebased ts=%d want 0", got)
	}
	if !bytes.Contains(out.Bytes(), []byte{'K'}) {
		t.Fatal("keyframe payload lost")
	}
}

func TestCatchupKeepsOnlyRecentLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.jsonl")
	body := "" +
		"{\"type\":\"meta\",\"name\":\"x\"}\n" +
		"{\"type\":\"gift\",\"ts\":1,\"user\":\"a\"}\n" +
		"{\"type\":\"danmaku\",\"ts\":10,\"text\":\"old\"}\n" +
		"{\"type\":\"gift\",\"ts\":100,\"user\":\"b\"}\n" +
		"{\"type\":\"danmaku\",\"ts\":120,\"text\":\"new\"}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := NewService().Catchup(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(bytes.Join(lines, []byte("\n")))
	if !bytes.Contains([]byte(got), []byte(`"meta"`)) {
		t.Fatalf("missing meta: %s", got)
	}
	if bytes.Contains([]byte(got), []byte(`"ts":1,`)) || bytes.Contains([]byte(got), []byte(`"ts":10,`)) {
		t.Fatalf("kept stale catchup: %s", got)
	}
	if !bytes.Contains([]byte(got), []byte(`"ts":100`)) || !bytes.Contains([]byte(got), []byte(`"ts":120`)) {
		t.Fatalf("dropped recent catchup: %s", got)
	}
}

func TestRebasePreviewTsShiftsToJoinClock(t *testing.T) {
	got := RebasePreviewTs([]byte(`{"type":"gift","ts":120.5}`), 120)
	if !bytes.Contains(got, []byte(`"ts":0.5`)) {
		t.Fatalf("ts not rebased: %s", got)
	}
	meta := []byte(`{"type":"meta","name":"x"}`)
	if !bytes.Equal(RebasePreviewTs(meta, 120), meta) {
		t.Fatal("meta must stay untouched")
	}
}

func setTagTS(tag []byte, ts uint32) {
	setFLVTagTS(tag, ts)
}
