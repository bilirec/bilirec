package metrics

import "time"

const (
	minEnqueueWaitRecord     = 10 * time.Millisecond
	slowFlushMetricThreshold = 3 * time.Second
	slowSyncMetricThreshold  = 1200 * time.Millisecond
)

// StreamObservationMeta is room identity for stream pipeline observers and alert payloads.
type StreamObservationMeta struct {
	Streamer  string
	RoomTitle string
}

// StreamObserver receives recording pipeline events for one room.
type StreamObserver interface {
	OnBytesWritten(roomID int, meta StreamObservationMeta, n int)
	OnTimelineGap(roomID int, meta StreamObservationMeta, delta time.Duration)
	OnSlowFlush(roomID int, meta StreamObservationMeta)
	OnSlowSync(roomID int, meta StreamObservationMeta)
	OnEnqueueWait(roomID int, meta StreamObservationMeta, wait time.Duration)
}

// StreamObservation fans out pipeline events to VictoriaMetrics and registered observers.
type StreamObservation struct {
	roomID    int
	meta      StreamObservationMeta
	observers []StreamObserver
}

// Register adds a process-wide stream observer. Call only during application wiring.
func (e *Exporter) Register(o StreamObserver) {
	if o == nil {
		return
	}
	e.observersMu.Lock()
	e.streamObservers = append(e.streamObservers, o)
	e.observersMu.Unlock()
}

// StreamObservation returns pipeline observers for one room.
func (e *Exporter) StreamObservation(roomID int, meta StreamObservationMeta) *StreamObservation {
	obs := &StreamObservation{
		roomID: roomID,
		meta:   meta,
	}
	e.observersMu.RLock()
	ext := len(e.streamObservers)
	e.observersMu.RUnlock()

	capN := ext
	if e.registry != nil {
		capN++
	}
	if capN == 0 {
		return obs
	}
	observers := make([]StreamObserver, 0, capN)
	if e.registry != nil {
		observers = append(observers, newStreamMetricsObserver(e.registry, roomID))
	}
	e.observersMu.RLock()
	observers = append(observers, e.streamObservers...)
	e.observersMu.RUnlock()
	obs.observers = observers
	return obs
}

// AddBytesWritten accumulates bytes passed to the segment writer Write().
func (s *StreamObservation) AddBytesWritten(n int) {
	if n <= 0 {
		return
	}
	for _, o := range s.observers {
		o.OnBytesWritten(s.roomID, s.meta, n)
	}
}

// RecordTimelineGap records a positive media timeline discontinuity.
func (s *StreamObservation) RecordTimelineGap(delta time.Duration) {
	if delta <= 0 {
		return
	}
	for _, o := range s.observers {
		o.OnTimelineGap(s.roomID, s.meta, delta)
	}
}

// RecordFlush records periodic or close flushes; slow flushes fan out to observers.
func (s *StreamObservation) RecordFlush(duration time.Duration) {
	if duration <= slowFlushMetricThreshold {
		return
	}
	for _, o := range s.observers {
		o.OnSlowFlush(s.roomID, s.meta)
	}
}

// RecordSync records fsync timing; slow syncs fan out to observers.
func (s *StreamObservation) RecordSync(duration time.Duration) {
	if duration <= slowSyncMetricThreshold {
		return
	}
	for _, o := range s.observers {
		o.OnSlowSync(s.roomID, s.meta)
	}
}

// RecordEnqueueWait records backpressure while the writer queue was full.
func (s *StreamObservation) RecordEnqueueWait(wait time.Duration) {
	if wait < minEnqueueWaitRecord {
		return
	}
	for _, o := range s.observers {
		o.OnEnqueueWait(s.roomID, s.meta, wait)
	}
}
