package metrics

import (
	vm "github.com/VictoriaMetrics/metrics"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

const (
	labelRoomID    = "room_id"
	labelUname     = "uname"
	labelEventType = "event_type"
	labelReason    = "reason"

	metricRoomInfo = "bilirec_room_info"
)

var logger = logrus.WithField("module", "metrics")

// Exporter owns the metrics set and the room registry. Its zero value is a
// safe no-op, which lets consumers use metrics without feature checks.
type Exporter struct {
	set      *vm.Set
	registry *roomRegistry
}

func provider(lc fx.Lifecycle, cfg *config.Config) *Exporter {
	if !cfg.MetricsEnabled {
		return &Exporter{}
	}

	registry := newRoomRegistry()
	exporter := &Exporter{
		set:      registry.set,
		registry: registry,
	}
	exporter.registerRuntimeGauges()
	exporter.registerServer(lc, cfg)
	return exporter
}

// DeleteRoom removes all metric series belonging to a room.
func (e *Exporter) DeleteRoom(roomID int) {
	if e.registry == nil {
		return
	}
	e.UnregisterRecorderRoom(roomID)
	e.UnregisterLiveRoom(roomID)
	e.UnregisterDanmakuRoom(roomID)
	e.registry.unregisterRoomInfo(roomID)
}

var Module = fx.Module("metrics", fx.Provide(provider))
