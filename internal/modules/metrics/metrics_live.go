package metrics

const (
	metricRoomLiveSessionsTotal = "bilirec_room_live_sessions_total"
	metricRoomLiveStatus        = "bilirec_room_live_status"
)

// SetLiveStatus updates the room's current live status and metadata.
func (e *Exporter) SetLiveStatus(roomID int, uname string, live bool) {
	if e.registry == nil {
		return
	}
	if live {
		e.registry.gauge(metricRoomLiveStatus, roomID).Set(1)
	} else {
		e.registry.gauge(metricRoomLiveStatus, roomID).Set(0)
	}
	e.registry.updateRoomInfo(roomID, uname)
}

// LiveSessionDetected records a newly detected live session.
func (e *Exporter) LiveSessionDetected(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.counter(metricRoomLiveSessionsTotal, roomID).Inc()
}

func (e *Exporter) UnregisterLiveRoom(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.unregisterCounter(metricRoomLiveSessionsTotal, roomID)
	e.registry.unregisterGauge(metricRoomLiveStatus, roomID)
}
