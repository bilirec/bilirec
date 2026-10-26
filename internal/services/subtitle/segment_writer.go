package subtitle

import (
	"context"
	"os"
	"path/filepath"

	"github.com/bilirec/bilirec/internal/processors"
	"github.com/bilirec/bilirec/pkg/pipeline"
	"github.com/bilirec/bilirec/pkg/sei"
	"github.com/bilirec/bilirec/utils"
)

type pendingCue struct {
	startMs int64
	endMs   int64
	text    string
}

// segmentWriter 在分段扫描 goroutine 里把 SEI 解析为 VTT cue 并写入 .vtt sidecar。
// 首条有效 cue 才创建文件并写入 "WEBVTT\n\n" 头；无 cue 则不落盘。
type segmentWriter struct {
	svc       *Service
	videoPath string

	writerCtx context.Context
	writer    *pipeline.Pipe[[]byte]

	seen   map[int64]struct{}
	baseMs int64
	baseOK bool
	last   *pendingCue
}

func newSegmentWriter(svc *Service, videoPath string) *segmentWriter {
	return &segmentWriter{
		svc:       svc,
		videoPath: videoPath,
		writerCtx: context.Background(),
		seen:      make(map[int64]struct{}),
	}
}

func (w *segmentWriter) enqueueFromAccessUnit(au accessUnit) {
	sei.ForEachRBSP(au.NALs, videoSEICodec(au.Codec), func(rbsp []byte) {
		if len(rbsp) == 0 || len(rbsp) > maxSEIRBSPBytes {
			return
		}
		for _, c := range cuesFromSEIRBSP(au.PTSMs, rbsp) {
			w.writeCue(c)
		}
	})
}

func (w *segmentWriter) finish() {
	if w.last != nil {
		w.flushLast(w.last.endMs)
	}
	if w.writer != nil {
		w.writer.Close()
		w.writer = nil
	}
}

func (w *segmentWriter) writeCue(c cue) {
	if _, seen := w.seen[c.ID]; seen {
		return
	}
	w.seen[c.ID] = struct{}{}

	if !w.baseOK {
		w.baseMs = c.PTSMs + c.OTMs
		w.baseOK = true
	}

	if w.writer == nil {
		if err := w.openWriter(); err != nil {
			log.Errorf("open subtitle sidecar failed path=%s err=%v", w.videoPath, err)
			return
		}
		// WebVTT 文件头（UTF-8 文本，无 BOM）。
		_, _ = w.writer.Process(w.writerCtx, []byte("WEBVTT\n\n"))
	}

	start := c.PTSMs + c.OTMs - w.baseMs
	end := start + c.DTMs
	if w.last != nil {
		w.flushLast(start)
	}
	w.last = &pendingCue{startMs: start, endMs: end, text: c.Text}
}

func (w *segmentWriter) flushLast(endMs int64) {
	if w.last == nil || w.writer == nil {
		return
	}
	if endMs < w.last.startMs {
		endMs = w.last.startMs
	}
	// 每条 cue 为一段 UTF-8 文本块，经 BufferedStreamWriter 落盘。
	buf := formatVTTCue(w.last.startMs, endMs, w.last.text)
	_, _ = w.writer.Process(w.writerCtx, buf)
	w.last = nil
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
