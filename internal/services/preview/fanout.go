package preview

import (
	"context"

	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/logger"
)

type fanoutProcessor struct {
	svc       *Service
	roomID    int
	path      string
	ext       string
	audioOnly bool
	room      *roomState
	det       *flv.HeaderChangeDetector
}

func (t *fanoutProcessor) Open(_ context.Context, _ logger.Logger) error {
	t.room = t.svc.bind(t.roomID, t.path, t.ext)
	if t.ext == ".flv" {
		t.det = flv.NewHeaderChangeDetector()
	}
	return nil
}

func (t *fanoutProcessor) Process(_ context.Context, _ logger.Logger, data []byte) ([]byte, error) {
	if len(data) == 0 || t.room == nil {
		return data, nil
	}
	if t.room.mediaN.Load() == 0 && t.room.bootstrapReady.Load() {
		return data, nil
	}
	if !t.room.bootstrapReady.Load() {
		t.captureBootstrap(data)
	}
	if t.room.mediaN.Load() == 0 {
		return data, nil
	}
	t.svc.fanout(t.roomID, data)
	return data, nil
}

func (t *fanoutProcessor) Close() error {
	if t.det != nil {
		t.det.Close()
		t.det = nil
	}
	t.svc.unbind(t.roomID, t.path)
	t.room = nil
	return nil
}

func (t *fanoutProcessor) captureBootstrap(data []byte) {
	if t.room == nil || t.room.bootstrapReady.Load() {
		return
	}
	switch t.ext {
	case ".flv":
		if t.det == nil {
			return
		}
		_ = t.det.DetectChange(data)
		vTag := flv.ZeroTagTimestamp(t.det.LastVideoTag())
		if len(vTag) > 0 {
			prefix := flv.NewFileHeaderBytes()
			prefix = append(prefix, vTag...)
			if aTag := flv.ZeroTagTimestamp(t.det.LastAudioTag()); len(aTag) > 0 {
				prefix = append(prefix, aTag...)
			}
			t.det.Close()
			t.det = nil
			t.publishBootstrap(prefix)
			return
		}
		if !t.audioOnly {
			return
		}
		aTag := flv.ZeroTagTimestamp(t.det.LastAudioTag())
		if len(aTag) == 0 {
			return
		}
		prefix := flv.NewFileHeaderBytesWithFlags(flv.TypeFlagAudio)
		prefix = append(prefix, aTag...)
		t.det.Close()
		t.det = nil
		t.publishBootstrap(prefix)
	case ".fmp4":
		if isFtyp(data) {
			t.publishBootstrap(append([]byte(nil), data...))
		}
	default:
		t.publishBootstrap(nil)
	}
}

func (t *fanoutProcessor) publishBootstrap(prefix []byte) {
	t.svc.mu.Lock()
	defer t.svc.mu.Unlock()
	r := t.svc.rooms[t.roomID]
	if r == nil || r.path != t.path || r.bootstrapReady.Load() {
		return
	}
	r.bootstrap = prefix
	r.bootstrapReady.Store(true)
}
