package preview

import (
	"context"

	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/pkg/pipeline"
)

func (s *Service) Tap(roomID int, path, ext string) *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"preview-tap",
		&tapProcessor{svc: s, roomID: roomID, path: path, ext: ext},
	)
}

type tapProcessor struct {
	svc    *Service
	roomID int
	path   string
	ext    string
}

func (t *tapProcessor) Open(_ context.Context, _ logger.Logger) error {
	if t.svc != nil {
		t.svc.bind(t.roomID, t.path, t.ext)
	}
	return nil
}

func (t *tapProcessor) Process(_ context.Context, _ logger.Logger, data []byte) ([]byte, error) {
	n := len(data)
	if n == 0 || t.svc == nil {
		return data, nil
	}
	indexHLS := t.ext == ".ts" || t.ext == ".fmp4"
	t.svc.observe(t.roomID, n, indexHLS, indexHLS && isFtyp(data))
	return data, nil
}

func (t *tapProcessor) Close() error {
	if t.svc != nil {
		t.svc.unbind(t.roomID, t.path)
	}
	return nil
}

func isFtyp(data []byte) bool {
	return len(data) >= 8 && string(data[4:8]) == "ftyp"
}
