package metrics

import (
	"fmt"
	"strconv"
	"sync"

	vm "github.com/VictoriaMetrics/metrics"
	"github.com/puzpuzpuz/xsync/v4"
)

// All fields are comparable, so these structs are valid xsync.Map keys.
type counterKey struct {
	name   string
	roomID int
	event  string
}

type gaugeKey struct {
	name   string
	roomID int
}

type roomEntry struct {
	infoName string
	uname    string
}

type roomRegistry struct {
	set      *vm.Set
	mu       sync.Mutex
	rooms    *xsync.Map[int, *roomEntry]
	counters *xsync.Map[counterKey, *vm.Counter]
	gauges   *xsync.Map[gaugeKey, *vm.Gauge]
}

func newRoomRegistry() *roomRegistry {
	return &roomRegistry{
		set:      vm.NewSet(),
		rooms:    xsync.NewMap[int, *roomEntry](),
		counters: xsync.NewMap[counterKey, *vm.Counter](),
		gauges:   xsync.NewMap[gaugeKey, *vm.Gauge](),
	}
}

func (r *roomRegistry) counter(name string, roomID int) *vm.Counter {
	key := counterKey{name: name, roomID: roomID}
	if counter, ok := r.counters.Load(key); ok {
		return counter
	}

	counter := r.set.GetOrCreateCounter(name + roomLabel(roomID))
	actual, _ := r.counters.LoadOrStore(key, counter)
	return actual
}

func (r *roomRegistry) counterEvent(name string, roomID int, eventType string) *vm.Counter {
	key := counterKey{name: name, roomID: roomID, event: eventType}
	if counter, ok := r.counters.Load(key); ok {
		return counter
	}

	counter := r.set.GetOrCreateCounter(name + roomEventLabel(roomID, eventType))
	actual, _ := r.counters.LoadOrStore(key, counter)
	return actual
}

func (r *roomRegistry) gauge(name string, roomID int) *vm.Gauge {
	key := gaugeKey{name: name, roomID: roomID}
	if gauge, ok := r.gauges.Load(key); ok {
		return gauge
	}

	gauge := r.set.GetOrCreateGauge(name+roomLabel(roomID), nil)
	actual, _ := r.gauges.LoadOrStore(key, gauge)
	return actual
}

func (r *roomRegistry) globalGauge(name string) *vm.Gauge {
	key := gaugeKey{name: name, roomID: 0}
	if gauge, ok := r.gauges.Load(key); ok {
		return gauge
	}

	gauge := r.set.GetOrCreateGauge(name, nil)
	actual, _ := r.gauges.LoadOrStore(key, gauge)
	return actual
}

func (r *roomRegistry) globalCounter(name string) *vm.Counter {
	key := counterKey{name: name}
	if counter, ok := r.counters.Load(key); ok {
		return counter
	}

	counter := r.set.GetOrCreateCounter(name)
	actual, _ := r.counters.LoadOrStore(key, counter)
	return actual
}

func (r *roomRegistry) providerCounter(name string, provider string) *vm.Counter {
	key := counterKey{name: name, event: provider}
	if counter, ok := r.counters.Load(key); ok {
		return counter
	}

	counter := r.set.GetOrCreateCounter(name + providerLabel(provider))
	actual, _ := r.counters.LoadOrStore(key, counter)
	return actual
}

func (r *roomRegistry) providerGauge(name string, provider string) *vm.Gauge {
	key := gaugeKey{name: name + provider, roomID: 0}
	if gauge, ok := r.gauges.Load(key); ok {
		return gauge
	}

	gauge := r.set.GetOrCreateGauge(name+providerLabel(provider), nil)
	actual, _ := r.gauges.LoadOrStore(key, gauge)
	return actual
}

func (r *roomRegistry) unregisterCounter(name string, roomID int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.set.UnregisterMetric(name + roomLabel(roomID))
	r.counters.Delete(counterKey{name: name, roomID: roomID})
}

func (r *roomRegistry) unregisterGauge(name string, roomID int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.set.UnregisterMetric(name + roomLabel(roomID))
	r.gauges.Delete(gaugeKey{name: name, roomID: roomID})
}

func (r *roomRegistry) unregisterCounterEvent(name string, roomID int, eventType string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.set.UnregisterMetric(name + roomEventLabel(roomID, eventType))
	r.counters.Delete(counterKey{name: name, roomID: roomID, event: eventType})
}

func (r *roomRegistry) counterReason(name string, roomID int, reason string) *vm.Counter {
	key := counterKey{name: name, roomID: roomID, event: reason}
	if counter, ok := r.counters.Load(key); ok {
		return counter
	}

	counter := r.set.GetOrCreateCounter(name + roomReasonLabel(roomID, reason))
	actual, _ := r.counters.LoadOrStore(key, counter)
	return actual
}

func (r *roomRegistry) unregisterCounterReason(name string, roomID int, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.set.UnregisterMetric(name + roomReasonLabel(roomID, reason))
	r.counters.Delete(counterKey{name: name, roomID: roomID, event: reason})
}

func (r *roomRegistry) unregisterRoomInfo(roomID int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if entry, ok := r.rooms.LoadAndDelete(roomID); ok && entry.infoName != "" {
		r.set.UnregisterMetric(entry.infoName)
	}
}

func (r *roomRegistry) updateRoomInfo(roomID int, uname string) {
	if uname == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	entry, _ := r.rooms.LoadOrCompute(roomID, func() (*roomEntry, bool) {
		return &roomEntry{}, false
	})
	if entry.uname == uname {
		return
	}
	if entry.infoName != "" {
		r.set.UnregisterMetric(entry.infoName)
	}

	entry.infoName = fmt.Sprintf(
		`%s{%s="%d",%s=%s}`,
		metricRoomInfo,
		labelRoomID,
		roomID,
		labelUname,
		strconv.Quote(uname),
	)
	entry.uname = uname
	r.set.NewGauge(entry.infoName, func() float64 { return 1 })
}

func roomLabel(roomID int) string {
	return fmt.Sprintf(`{%s="%d"}`, labelRoomID, roomID)
}

func roomEventLabel(roomID int, eventType string) string {
	return fmt.Sprintf(
		`{%s="%d",%s=%s}`,
		labelRoomID,
		roomID,
		labelEventType,
		strconv.Quote(eventType),
	)
}

func roomReasonLabel(roomID int, reason string) string {
	return fmt.Sprintf(
		`{%s="%d",%s=%s}`,
		labelRoomID,
		roomID,
		labelReason,
		strconv.Quote(reason),
	)
}

func providerLabel(provider string) string {
	return fmt.Sprintf(`{provider=%s}`, strconv.Quote(provider))
}
