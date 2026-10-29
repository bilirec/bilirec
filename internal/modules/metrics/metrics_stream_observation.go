package metrics

import "time"

const (
	MinEnqueueWaitRecord     = 10 * time.Millisecond
	SlowFlushMetricThreshold = 3 * time.Second
	SlowSyncMetricThreshold  = 1200 * time.Millisecond
)

// StreamObservationMeta is room identity for stream pipeline observers and alert payloads.
type StreamObservationMeta struct {
	Streamer  string
	RoomTitle string
}

// StreamObserver receives recording pipeline events for one room.
type StreamObserver interface {
	OnBytesRead(roomID int, meta StreamObservationMeta, n int)
	OnBytesWritten(roomID int, meta StreamObservationMeta, n int)
	OnTimelineGap(roomID int, meta StreamObservationMeta, delta time.Duration)
	OnFlush(roomID int, meta StreamObservationMeta, duration time.Duration)
	OnSync(roomID int, meta StreamObservationMeta, duration time.Duration)
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
// Optional local observers are session-scoped (e.g. recorder stats); global Register observers are appended after.
func (e *Exporter) StreamObservation(roomID int, meta StreamObservationMeta, local ...StreamObserver) *StreamObservation {
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
	if capN == 0 && len(local) == 0 {
		return obs
	}
	observers := make([]StreamObserver, 0, capN+len(local))
	if e.registry != nil {
		observers = append(observers, newStreamMetricsObserver(e.registry, roomID))
	}
	observers = append(observers, local...)
	e.observersMu.RLock()
	observers = append(observers, e.streamObservers...)
	e.observersMu.RUnlock()
	obs.observers = observers
	return obs
}

// AddBytesRead accumulates bytes read from the live stream ingest path.
func (s *StreamObservation) AddBytesRead(n int) {
	if n <= 0 {
		return
	}
	for _, o := range s.observers {
		o.OnBytesRead(s.roomID, s.meta, n)
	}
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

// RecordFlush records periodic or close flushes; observers apply their own thresholds.
func (s *StreamObservation) RecordFlush(duration time.Duration) {
	if duration <= 0 {
		return
	}
	for _, o := range s.observers {
		o.OnFlush(s.roomID, s.meta, duration)
	}
}

// RecordSync records fsync timing; observers apply their own thresholds.
func (s *StreamObservation) RecordSync(duration time.Duration) {
	if duration <= 0 {
		return
	}
	for _, o := range s.observers {
		o.OnSync(s.roomID, s.meta, duration)
	}
}

// RecordEnqueueWait records backpressure while the writer queue was full.
func (s *StreamObservation) RecordEnqueueWait(wait time.Duration) {
	if wait < MinEnqueueWaitRecord {
		return
	}
	for _, o := range s.observers {
		o.OnEnqueueWait(s.roomID, s.meta, wait)
	}
}
