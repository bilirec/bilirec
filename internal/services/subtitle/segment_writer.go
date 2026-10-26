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
	id      int64
	startMs int64
	endMs   int64
	text    string
}

// segmentWriter 在分段扫描期间按 sub_id 合并 SEI（同一句的后续更新覆盖正文），
// finish 时才写入 .vtt。首条有效 cue 才创建文件；无 cue 则不落盘。
type segmentWriter struct {
	svc       *Service
	videoPath string

	writerCtx context.Context
	writer    *pipeline.Pipe[[]byte]

	cues   []pendingCue
	index  map[int64]int
	baseMs int64
	baseOK bool
}

func newSegmentWriter(svc *Service, videoPath string) *segmentWriter {
	return &segmentWriter{
		svc:       svc,
		videoPath: videoPath,
		writerCtx: context.Background(),
		index:     make(map[int64]int),
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
	if len(w.cues) == 0 {
		return
	}
	if err := w.openWriter(); err != nil {
		log.Errorf("open subtitle sidecar failed path=%s err=%v", w.videoPath, err)
		return
	}
	_, _ = w.writer.Process(w.writerCtx, []byte("WEBVTT\n\n"))
	for i, c := range w.cues {
		end := c.endMs
		if i+1 < len(w.cues) {
			end = w.cues[i+1].startMs
		}
		if end < c.startMs {
			end = c.startMs
		}
		_, _ = w.writer.Process(w.writerCtx, formatVTTCue(c.startMs, end, c.text))
	}
	w.writer.Close()
	w.writer = nil
}

func (w *segmentWriter) writeCue(c cue) {
	if !w.baseOK {
		w.baseMs = c.PTSMs + c.OTMs
		w.baseOK = true
	}
	start := c.PTSMs + c.OTMs - w.baseMs
	end := start + c.DTMs
	if i, ok := w.index[c.ID]; ok {
		w.cues[i].text = c.Text
		w.cues[i].endMs = end
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
