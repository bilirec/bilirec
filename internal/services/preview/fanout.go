package preview

import (
	"context"

	"github.com/bilirec/bilirec/pkg/logger"
)

type fanoutProcessor struct {
	svc    *Service
	roomID int
	path   string
	ext    string
	room   *roomState
}

func (t *fanoutProcessor) Open(_ context.Context, _ logger.Logger) error {
	t.room = t.svc.bind(t.roomID, t.path, t.ext)
	return nil
}

func (t *fanoutProcessor) Process(_ context.Context, _ logger.Logger, data []byte) ([]byte, error) {
	if len(data) == 0 || t.room == nil {
		return data, nil
	}
	if t.room.mediaN.Load() == 0 && t.room.bootstrapReady.Load() {
		return data, nil
	}
	t.svc.fanout(t.roomID, data)
	return data, nil
}

func (t *fanoutProcessor) Close() error {
	t.svc.unbind(t.roomID, t.path)
	t.room = nil
	return nil
}
