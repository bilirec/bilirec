package subtitle

import (
	"context"
	"path/filepath"
	"sync/atomic"

	"github.com/bilirec/bilirec/pkg/ds"
	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/pkg/pipeline"
)

const subtitleQueueMaxBytes = 4 << 20

// accessUnit 表示一帧视频在字幕管线里的最小单位：带 PTS 的 NAL 字节块。
// NALs 的封装格式因来源而异，后续 SEI 扫描会先判断 AVCC 还是 Annex-B。
type accessUnit struct {
	PTSMs int64
	Codec flv.VideoKind
	// AVCC：4 字节大端长度 + NAL 体，可连续多个 NAL。
	// Annex-B：0x000001 / 0x00000001 起始码分隔的 NAL（HLS TS 的 PES 负载常见）。
	NALs []byte
}

// NewPipelineProcessor records AI subtitles into a .vtt sidecar beside videoPath.
func (s *Service) NewPipelineProcessor(videoPath string) *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"subtitle-writer",
		&segmentProcessor{
			svc:       s,
			videoPath: videoPath,
			ext:       filepath.Ext(videoPath),
		},
	)
}

type segmentProcessor struct {
	svc       *Service
	videoPath string
	ext       string
	seg       *segmentWriter
	q         *ds.ChunkQueue[queuedChunk]
	done      chan struct{}

	drops atomic.Uint64
	gap   atomic.Bool
}

type fmp4Scratch struct {
	st fmp4State
}

type tsScratch struct {
	videoPID uint16
	codec    flv.VideoKind
	pesBuf   []byte
	pesPTS   uint64
	havePES  bool
}

type queuedChunk struct {
	data []byte
	gap  bool
}

func applyChunkGap(sc *tsScratch) {
	sc.pesBuf = nil
	sc.havePES = false
}

func (p *segmentProcessor) Open(_ context.Context, _ logger.Logger) error {
	p.seg = newSegmentWriter(p.svc, p.videoPath)
	p.q = ds.NewChunkQueue(subtitleQueueMaxBytes,
		func(c queuedChunk) int { return len(c.data) },
		ds.AllowOversizeWhenEmpty(),
	)
	p.done = make(chan struct{})
	p.svc.idle.Add(1)
	p.svc.track(p.videoPath, p.done)
	go p.consume(p.q)
	return nil
}

// Process 透传原始录制字节；扫描在 consume goroutine 中异步进行（不修改 data）。
func (p *segmentProcessor) Process(_ context.Context, _ logger.Logger, data []byte) ([]byte, error) {
	if p.q == nil || len(data) == 0 {
		return data, nil
	}
	if !p.q.CanTake(len(data)) {
		if p.drops.Add(1) == 1 {
			log.Warnf("字幕扫描队列已满 path=%s chunk=%d queued=%d",
				p.videoPath, len(data), p.q.QueuedBytes())
		}
		p.gap.Store(true)
		return data, nil
	}
	owned := append([]byte(nil), data...)
	p.q.Push(queuedChunk{data: owned, gap: p.gap.Swap(false)})
	return data, nil
}

func (p *segmentProcessor) Close() error {
	if p.q != nil {
		p.q.Close()
		p.q = nil
	}
	return nil
}

func (p *segmentProcessor) consume(q *ds.ChunkQueue[queuedChunk]) {
	var fmp4 fmp4Scratch
	var ts tsScratch
	defer func() {
		if rec := recover(); rec != nil {
			log.Errorf("subtitle scan panicked path=%s err=%v", p.videoPath, rec)
		}
		if p.ext == ".ts" {
			if au, ok := flushTSPES(&ts); ok {
				p.seg.enqueueFromAccessUnit(au)
			}
		}
		if drops := p.drops.Load(); drops > 0 {
			log.Warnf("字幕扫描本段丢弃 chunk 数 path=%s drops=%d", p.videoPath, drops)
		}
		p.seg.finish()
		close(p.done)
		p.svc.untrack(p.videoPath, p.done)
		p.svc.idle.Done()
	}()
	for {
		item, ok := q.Pop()
		if !ok {
			break
		}
		if item.gap && p.ext == ".ts" {
			applyChunkGap(&ts)
		}
		switch p.ext {
		case ".flv":
			for _, au := range accessUnitsFromFLVChunk(item.data) {
				p.seg.enqueueFromAccessUnit(au)
			}
		case ".fmp4":
			for _, au := range accessUnitsFromFMP4Chunk(item.data, &fmp4.st) {
				p.seg.enqueueFromAccessUnit(au)
			}
		case ".ts":
			for _, au := range accessUnitsFromTSChunk(item.data, &ts) {
				p.seg.enqueueFromAccessUnit(au)
			}
		}
	}
}
