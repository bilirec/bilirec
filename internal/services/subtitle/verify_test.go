package subtitle

import (
	"cmp"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bilirec/bilirec/utils"
)

// verifyToleranceMs 是 VTT 与文件内重算时间轴的容差。
// FLV tag 时间戳毫秒精度 + keyframe 抖动，40ms 足够宽而不掩盖真实偏差。
const verifyToleranceMs = 40

// segmentZeroSlackMs 守「FLV/fMP4 分段内 PTS 已归零」的前提。
// realtime_fixer / fmp4_timestamp_normalizer 把 DTS 归零后，残余偏移只有
// B 帧 composition time（实测 < 1s）；若上游归零失效，PTS 会是源 epoch
// （分钟到小时级），量级完全不同，该门限能干净区分两种状态。
const segmentZeroSlackMs = 2000

type verifyCue struct {
	ID      int64
	Text    string
	StartMs int64 // 预期 start = PTS + ot_ms（TS 已减首 AU PTS，未 clamp）
	DTMs    int64
}

// TestVerifySubtitleTimeline 校验 <video> 与其 .vtt 旁车的时间轴是否一致。
// 断言基准是「从视频字节独立重算」的 PTS + ot_ms，而不是写盘端自己的结果，
// 因此写盘端多减/少减基准、TS 未锚定、过期重发拉长末条都会被它抓住。
//
// 环境变量（缺省跳过，防 CI 误跑）：
//   - BILIREC_SUBTITLE_VERIFY_SAMPLES=dir  目录内所有 .flv/.fmp4/.ts 逐份校验（含子目录）
//   - BILIREC_SUBTITLE_VERIFY_FILE=path    单个视频文件校验
//   - BILIREC_SUBTITLE_VERIFY_REPLAY=1     额外用现行 writer 重放同一份输入，校验产出（离线绿队）
//   - BILIREC_SUBTITLE_VERIFY_ON_DISK=1    重放模式下也校验盘上既有 .vtt（实录像用）
func TestVerifySubtitleTimeline(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	replay := os.Getenv("BILIREC_SUBTITLE_VERIFY_REPLAY") == "1"
	if path := strings.TrimSpace(os.Getenv("BILIREC_SUBTITLE_VERIFY_FILE")); path != "" {
		t.Run(filepath.Base(path), func(t *testing.T) {
			verifySegmentFile(t, path, replay)
		})
		return
	}
	dir := strings.TrimSpace(os.Getenv("BILIREC_SUBTITLE_VERIFY_SAMPLES"))
	if dir == "" {
		t.Skip("set BILIREC_SUBTITLE_VERIFY_SAMPLES or BILIREC_SUBTITLE_VERIFY_FILE")
	}
	files := subtitleVideoFiles(t, dir)
	if len(files) == 0 {
		t.Skipf("no .flv/.fmp4/.ts sample under %s", dir)
	}
	for _, video := range files {
		t.Run(filepath.Base(video), func(t *testing.T) {
			verifySegmentFile(t, video, replay)
		})
	}
}

func subtitleVideoFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch filepath.Ext(path) {
		case ".flv", ".fmp4", ".ts":
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return files
}

// verifySegmentFile 对单份分段做三件事：
//  1. 前提守门：FLV/fMP4 文件内 PTS 已归零（minPTS 近 0）；TS 能解析出视频 AU
//  2. 用现行 writer 重放输入（replay=true 时），校验产出与重算一致
//  3. 校验盘上既有 .vtt 与重算一致
func verifySegmentFile(t *testing.T, videoPath string, replay bool) {
	t.Helper()
	data, err := os.ReadFile(videoPath)
	if err != nil {
		t.Fatalf("read %s: %v", videoPath, err)
	}
	tl := recomputeExpectedCues(t, videoPath, data)
	if len(tl.cues) == 0 {
		t.Skipf("no subtitle cues reparsed from %s (quiet segment)", filepath.Base(videoPath))
	}
	checkAnchorPremise(t, filepath.Ext(videoPath), tl)
	if replay {
		dir := t.TempDir()
		out := filepath.Join(dir, "replay"+filepath.Ext(videoPath))
		replaySegment(t, videoPath, data, out)
		assertVTTMatches(t, out, tl, "replay")
		// 基线样本的 .vtt 由旧版本产出。重放模式下默认只校验现行 writer 的产出，
		// 需要同时校验盘上旁车时显式设 BILIREC_SUBTITLE_VERIFY_ON_DISK=1（绿队实录像用）。
		if os.Getenv("BILIREC_SUBTITLE_VERIFY_ON_DISK") != "1" {
			return
		}
	}

	vttPath := utils.SidecarPathForVideo(videoPath, ".vtt")
	if _, err := os.Stat(vttPath); err != nil {
		t.Skipf("no on-disk vtt beside %s", filepath.Base(videoPath))
	}
	assertVTTMatches(t, videoPath, tl, "on-disk")
}

// checkAnchorPremise 守重算基准成立的前提；前提破坏时先失败并指向上游，
// 而不是让后续比对产生无意义的巨大偏差。
func checkAnchorPremise(t *testing.T, ext string, tl recomputedTimeline) {
	t.Helper()
	switch ext {
	case ".flv", ".fmp4":
		// baseMs=0 的前提：文件内 PTS 已被上游归零。minPTS 只带 B 帧 CTS 残余；
		// 归零失效时是源 epoch（分钟级以上）。
		if tl.firstVideoAUPTS < 0 || tl.minPTS < 0 || tl.minPTS >= segmentZeroSlackMs {
			t.Fatalf("anchor premise broken: minPTS=%d firstAUPTS=%d (want 0 <= minPTS < %d, i.e. segment PTS normalized to 0); check upstream normalizer before blaming segmentWriter",
				tl.minPTS, tl.firstVideoAUPTS, segmentZeroSlackMs)
		}
	case ".ts":
		// TS 无视频 AU 时无法锚定 baseMs，前提独立成立。
		if tl.firstVideoAUPTS < 0 {
			t.Fatalf("no video access unit reparsed from ts segment; cannot anchor baseMs (PMT/PES parse broken?)")
		}
	}
}

// replaySegment 把同一份输入字节按现行 writer 语义重放，产出 <out>.vtt。
func replaySegment(t *testing.T, videoPath string, data []byte, out string) {
	t.Helper()
	w := newSegmentWriter(testSubtitleService(), out)
	ext := filepath.Ext(videoPath)
	switch ext {
	case ".flv":
		// 喂原始落盘字节（含 13 字节文件前导）：accessUnitsFromFLVChunk 会
		// 逐字节重同步越过非 tag 前缀，与生产 chunk 喂入走同一解析路径。
		for _, au := range accessUnitsFromFLVChunk(data) {
			w.enqueueFromAccessUnit(au)
		}
	case ".fmp4":
		// parseInit 只在含 ftyp/moov 的片段更新 track/timescale；按 moof 边界切块喂入。
		// 注意：fmp4State 必须跨 chunk 保留（init 信息在第一个 chunk），与生产一致。
		var st fmp4State
		for _, chunk := range splitFMP4Chunks(data) {
			for _, au := range accessUnitsFromFMP4Chunk(chunk, &st) {
				w.enqueueFromAccessUnit(au)
			}
		}
	case ".ts":
		sc := &tsScratch{}
		for _, au := range accessUnitsFromTSChunk(data, sc) {
			w.enqueueFromAccessUnit(au)
		}
		if au, ok := flushTSPES(sc); ok {
			w.enqueueFromAccessUnit(au)
		}
	default:
		t.Fatalf("unsupported ext %q", ext)
	}
	w.finish()
}

// assertVTTMatches 逐条比对 .vtt 与重算结果。
func assertVTTMatches(t *testing.T, videoPath string, tl recomputedTimeline, label string) {
	t.Helper()
	vttPath := utils.SidecarPathForVideo(videoPath, ".vtt")
	vttData, err := os.ReadFile(vttPath)
	if err != nil {
		t.Fatalf("%s: read vtt: %v", label, err)
	}
	got := parseVTTCues(t, string(vttData))
	expected := tl.cues
	if len(got) != len(expected) {
		t.Fatalf("%s: cue count mismatch: vtt=%d expected=%d\nvtt=%v\nexpected=%v",
			label, len(got), len(expected), got, expected)
	}

	// cue start 的回退上限 = 流自身相邻 AU 的最大 PTS 回退（B 帧重排的物理
	// 窗口）+ 比对容差。SEI 挂在解码序的帧上，PTS 允许小幅回退；无重排的流
	// maxPTSRegress=0，检查自动收紧为「递增，容差内抖动」。expected 在比对前
	// 已按起点稳定排过（与 writer 的 finish 一致），因此残留回退只可能来自
	// B 帧重排，或 writer 未按起点排序；跨 sub_id 的 ASR 乱序由排序消化。
	allowRegress := tl.maxPTSRegress + int64(verifyToleranceMs)
	prevStart := int64(-1)
	for i, e := range expected {
		// 跨分段边界的句子 start 可能为负；写盘端 formatVTTTime 会 clamp 到 0，
		// 因此比较基准按 clamp 后的值判断。
		wantStart := max64(e.StartMs, 0)
		if prevStart >= 0 && wantStart < prevStart-allowRegress {
			t.Fatalf("%s: cue start regresses beyond reorder window at cue[%d] id=%d: %d < %d (maxPTSRegress=%d)",
				label, i, e.ID, wantStart, prevStart, tl.maxPTSRegress)
		}
		prevStart = max64(prevStart, wantStart)

		if diff := abs64(got[i].start - wantStart); diff > verifyToleranceMs {
			t.Fatalf("%s cue[%d] id=%d text=%q: vtt start=%d, expected max(0,%d)=%d (diff=%dms)",
				label, i, e.ID, e.Text, got[i].start, e.StartMs, wantStart, diff)
		}

		if got[i].end < got[i].start {
			t.Fatalf("%s cue[%d] end < start: %d < %d", label, i, got[i].end, got[i].start)
		}
		// 末条时长检查：endMs 不应被过期重发拉长（start+dt+容差）。
		if i == len(expected)-1 {
			maxEnd := wantStart + e.DTMs + int64(verifyToleranceMs)
			if got[i].end > maxEnd {
				t.Fatalf("%s last cue end=%d exceeds start+dt=%d (stale replay polluted endMs?)",
					label, got[i].end, maxEnd)
			}
		}
	}
}

// expectedEntry 是重算阶段追踪一条字幕的状态。
type expectedEntry struct {
	id      int64
	startMs int64 // 绝对 expected start（TS 已减首 AU PTS）
	dtMs    int64 // 当前接受的 dt（合并语义下的最大值）
	text    string
}

// recomputedTimeline 是从视频字节独立重算出的时间轴与前提数据。
type recomputedTimeline struct {
	// firstVideoAUPTS 是首个（视频）AU 的 PTS，-1 表示无 AU。writer 的 TS 锚点
	// 语义正是「首个视频 AU」（三个解析器都只产出视频 AU），这里刻意镜像它。
	firstVideoAUPTS int64
	// minPTS 是全体 AU 的最小 PTS；FLV/fMP4 归零前提下应近 0。
	minPTS int64
	// maxPTSRegress 是解码顺序下相邻 AU 的最大 PTS 回退（B 帧重排的物理窗口）。
	maxPTSRegress int64
	cues          []verifyCue
}

// recomputeExpectedCues 复刻 writer 的接受顺序与合并语义，但基准独立取自文件本身：
// FLV/fMP4 baseMs=0（上游已归零），TS baseMs=首个视频 AU 的 PTS。
// FLV 喂原始落盘字节（重同步越过文件前导，与生产同路径）；
// fmp4 按 moof 边界切块且 fmp4State 跨 chunk 保留（init 在第一个 chunk）。
func recomputeExpectedCues(t *testing.T, videoPath string, data []byte) recomputedTimeline {
	t.Helper()
	var entries []expectedEntry
	index := make(map[int64]int)
	baseMs := int64(0)
	tl := recomputedTimeline{firstVideoAUPTS: -1, minPTS: -1}
	prevAUPts := int64(0)
	epochBase := filepath.Ext(videoPath) == ".ts"

	accept := func(au accessUnit) {
		if tl.firstVideoAUPTS < 0 {
			tl.firstVideoAUPTS = au.PTSMs
			if epochBase {
				baseMs = au.PTSMs
			}
		} else if r := prevAUPts - au.PTSMs; r > tl.maxPTSRegress {
			tl.maxPTSRegress = r
		}
		prevAUPts = au.PTSMs
		if tl.minPTS < 0 || au.PTSMs < tl.minPTS {
			tl.minPTS = au.PTSMs
		}
		for _, c := range cuesFromSEI(au) {
			start := c.PTSMs + c.OTMs - baseMs
			end := start + c.DTMs
			if i, ok := index[c.ID]; ok {
				cur := &entries[i]
				// 与 writeCue 一致：正文一律采用最新（空正文已在 cuesFromSEI 滤掉）。
				cur.text = c.Text
				if start <= cur.startMs+staleReplayDriftMs && end > cur.startMs+cur.dtMs {
					cur.dtMs = end - cur.startMs
				}
				continue
			}
			index[c.ID] = len(entries)
			entries = append(entries, expectedEntry{id: c.ID, startMs: start, dtMs: c.DTMs, text: c.Text})
		}
	}

	switch filepath.Ext(videoPath) {
	case ".flv":
		// 喂原始落盘字节（含 13 字节文件前导）：与生产同一条解析路径，
		// accessUnitsFromFLVChunk 重同步越过非 tag 前缀。
		for _, au := range accessUnitsFromFLVChunk(data) {
			accept(au)
		}
	case ".fmp4":
		// fmp4State 跨 chunk 保留（init 信息在第一个 chunk）。
		var st fmp4State
		for _, chunk := range splitFMP4Chunks(data) {
			for _, au := range accessUnitsFromFMP4Chunk(chunk, &st) {
				accept(au)
			}
		}
	case ".ts":
		sc := &tsScratch{}
		for _, au := range accessUnitsFromTSChunk(data, sc) {
			accept(au)
		}
		if au, ok := flushTSPES(sc); ok {
			accept(au)
		}
	default:
		t.Fatalf("unsupported ext %q", filepath.Ext(videoPath))
	}

	out := make([]verifyCue, 0, len(entries))
	for _, e := range entries {
		out = append(out, verifyCue{ID: e.id, Text: e.text, StartMs: e.startMs, DTMs: e.dtMs})
	}
	// 与 writer 的 finish 一样按起点稳定排序：到达序不保证时间序，比对基准
	// 必须是写盘后 VTT 的实际顺序。maxPTSRegress 已按解码序算完，不受影响。
	slices.SortStableFunc(out, func(a, b verifyCue) int {
		return cmp.Compare(a.StartMs, b.StartMs)
	})
	tl.cues = out
	return tl
}

// splitFMP4Chunks 按 moof 盒子边界把整段字节切成多个片段，逐段喂给解析器。
func splitFMP4Chunks(data []byte) [][]byte {
	var out [][]byte
	prev := 0
	for i := 0; i+8 <= len(data); {
		size := int(uint32(data[i])<<24 | uint32(data[i+1])<<16 | uint32(data[i+2])<<8 | uint32(data[i+3]))
		typ := string(data[i+4 : i+8])
		if size < 8 || i+size > len(data) {
			break
		}
		if typ == "moof" && i > prev {
			out = append(out, data[prev:i])
			prev = i
		}
		i += size
	}
	out = append(out, data[prev:])
	return out
}

type vttCue struct {
	start int64
	end   int64
}

// parseVTTCues 解析 VTT 文件的 cue 时间行（毫秒）。
func parseVTTCues(t *testing.T, content string) []vttCue {
	t.Helper()
	var out []vttCue
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "-->") {
			continue
		}
		parts := strings.SplitN(line, "-->", 2)
		out = append(out, vttCue{start: parseVTTTimeMs(t, parts[0]), end: parseVTTTimeMs(t, parts[1])})
	}
	return out
}

func parseVTTTimeMs(t *testing.T, s string) int64 {
	t.Helper()
	s = strings.TrimSpace(s)
	var h, m, sec, ms int
	if _, err := fmt.Sscanf(s, "%02d:%02d:%02d.%03d", &h, &m, &sec, &ms); err != nil {
		t.Fatalf("parse vtt time %q: %v", s, err)
	}
	return int64(h)*3_600_000 + int64(m)*60_000 + int64(sec)*1000 + int64(ms)
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
