package subtitle

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"

	"github.com/bilirec/bilirec/internal/processors"
	"github.com/bilirec/bilirec/pkg/pipeline"
	"github.com/bilirec/bilirec/utils"
)

type pendingCue struct {
	id      int64
	startMs int64
	endMs   int64
	text    string
}

// segmentWriter 在分段扫描期间按 sub_id 合并 SEI（同一句的后续更新覆盖正文），
// finish 时才写入 .vtt。首条有效 cue 才创建文件；无 cue 则不落盘。
//
// 时间轴基准与上游 processor 顺序耦合：
//   - .flv / .fmp4 上游已把分段时间戳归一为从 0 开始（realtime_fixer /
//     fmp4_timestamp_normalizer），字幕 PTS 即文件内绝对毫秒，baseMs=0。
//   - .ts 的 PES PTS 保留源 epoch，需减去首个视频 AU 的 PTS 才是文件内时间。
// 若日后调整 processor 或轮转归零行为，必须同步检查这里。
type segmentWriter struct {
	svc       *Service
	videoPath string

	writerCtx context.Context
	writer    *pipeline.Pipe[[]byte]

	epochBase bool
	baseMs    int64
	baseOK    bool

	cues  []pendingCue
	index map[int64]int

	// held 在 .ts 的 timeline 錨點尚未確定時暫存 cue（PTS 為源 epoch 的絕對值），
	// 錨點出現後再換算寫入。FLV/fMP4 的 epochBase 為 false，永不使用。
	held []cue
}

func newSegmentWriter(svc *Service, videoPath string) *segmentWriter {
	return &segmentWriter{
		svc:       svc,
		videoPath: videoPath,
		writerCtx: context.Background(),
		epochBase: filepath.Ext(videoPath) == ".ts",
		index:     make(map[int64]int),
	}
}

func (w *segmentWriter) enqueueFromAccessUnit(au accessUnit) {
	if w.epochBase && !w.baseOK && au.ptsKnown {
		w.baseMs = au.PTSMs
		w.baseOK = true
		w.flushHeld()
	}
	cues := cuesFromSEI(au)
	if w.epochBase && !w.baseOK {
		// 錨點未定：PTS 可能只是 0 佔位，先暫存不寫，等真正的首個視頻 PTS。
		w.held = append(w.held, cues...)
		return
	}
	for _, c := range cues {
		w.writeCue(c)
	}
}

// flushHeld 把暫存的 cue 按已確定的 baseMs 寫入。
func (w *segmentWriter) flushHeld() {
	held := w.held
	w.held = nil
	for _, c := range held {
		w.writeCue(c)
	}
}

func (w *segmentWriter) finish() {
	if w.epochBase && !w.baseOK {
		// 整段都沒有可用的 PTS：以 0 為基準吐出暫存 cue，避免字幕整段消失。
		w.baseMs = 0
		w.baseOK = true
		w.flushHeld()
	}
	if len(w.cues) == 0 {
		return
	}
	// 到達序不保證時間序：ASR 後到的 sub_id 起點可能早於前一條。按起點穩定排序
	// 後，「下一條起點當結束時間」才不會把上一條收成零時長；同起點保持到達序。
	// 同起點的句子不互相截成零時長：截到後面第一個更晚的顯示起點，沒有才留 endMs。
	slices.SortStableFunc(w.cues, func(a, b pendingCue) int {
		return cmp.Compare(a.startMs, b.startMs)
	})
	if err := w.openWriter(); err != nil {
		log.Errorf("open subtitle sidecar failed path=%s err=%v", w.videoPath, err)
		return
	}
	_, _ = w.writer.Process(w.writerCtx, []byte("WEBVTT\n\n"))
	for i, c := range w.cues {
		// 寫盤端以 0 為最早起點（formatVTTTime 會 clamp 負數），這裡先收一次，
		// 後面的截斷比較才不會被負起點騙過去。
		start := c.startMs
		if start < 0 {
			start = 0
		}
		end := c.endMs
		// 整句都在 0 之前（endMs <= 起點）沒有可見部分，維持零時長，不要被後面的
		// 句子拉出來顯示。
		if end > start {
			// 往後找第一個顯示起點更晚的 cue，用它當結束時間（非末條原本就延長到
			// 下一條起點）。緊鄰的下一條可能同樣被 clamp 到 0，用它截斷會把這條收成
			// 零時長；找不到更晚的就用自己的 endMs，尾端允許重疊。
			for j := i + 1; j < len(w.cues); j++ {
				next := w.cues[j].startMs
				if next < 0 {
					next = 0
				}
				if next > start {
					end = next
					break
				}
			}
		}
		if end < start {
			end = start
		}
		_, _ = w.writer.Process(w.writerCtx, formatVTTCue(start, end, c.text))
	}
	w.writer.Close()
	w.writer = nil
}

// staleReplayDriftMs 允许同一 sub_id 重发时 start 的正常抖动范围。
// 超过该幅度视为句末过期重发（B 站 ASR 会延迟重发已完成句子且 start 漂移），
// 用它的 start+dt 延伸 endMs 会把字幕拖长到数十秒。
const staleReplayDriftMs = 500

func (w *segmentWriter) writeCue(c cue) {
	start := c.PTSMs + c.OTMs - w.baseMs
	end := start + c.DTMs
	if i, ok := w.index[c.ID]; ok {
		cur := &w.cues[i]
		// 正文一律採用最新（空正文在 cuesFromSEI 已濾掉），句末重發常帶最終文本。
		cur.text = c.Text
		// endMs 只在未漂移的重發上延伸：句末過期重發的 start 明顯後移，
		// 用它的 start+dt 會把字幕拖長到數十秒。
		if start <= cur.startMs+staleReplayDriftMs && end > cur.endMs {
			cur.endMs = end
		}
		return
	}
	w.index[c.ID] = len(w.cues)
	w.cues = append(w.cues, pendingCue{
		id:      c.ID,
		startMs: start,
		endMs:   end,
		text:    c.Text,
	})
}

func (w *segmentWriter) openWriter() error {
	p := utils.SidecarPathForVideo(w.videoPath, ".vtt")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	opts := w.svc.writer
	writerPipeline := pipeline.New(
		processors.NewBufferedStreamWriter(
			p,
			processors.WithSkipSmallFlushThreshold(0),
			processors.WithBufferSize(opts.bufferSize),
			processors.WithFlushPeriod(opts.flushPeriod),
			processors.WithSyncPeriod(opts.syncPeriod),
			processors.WithMinPeriodicFlushBytes(opts.minPeriodicFlushBytes),
			processors.WithSequentialWrite(opts.sequentialWrite),
		),
	)
	if err := writerPipeline.Open(w.writerCtx); err != nil {
		return err
	}
	w.writer = writerPipeline
	return nil
}
