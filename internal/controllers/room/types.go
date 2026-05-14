package room

type LiveInfo struct {
	RoomId int  `json:"room_id"`
	IsLive bool `json:"is_live"`
}

type BatchRoomIDsRequest struct {
	RoomIDs []int `json:"roomIDs"`
}

type SubscribeStatus struct {
	RoomId       int  `json:"room_id"`
	IsSubscribed bool `json:"is_subscribed"`
}

type SubscribeList struct {
	RoomIds []int `json:"room_ids"`
}

type RoomConfigResponse struct {
	RoomId                int  `json:"room_id"`
	AutoRecord            bool `json:"auto_record"`
	Notify                bool `json:"notify"`
	RecordDurationMinutes int  `json:"record_duration_minutes"`
}

type UpdateRoomConfigRequest struct {
	AutoRecord            bool `json:"auto_record"`
	Notify                bool `json:"notify"`
	RecordDurationMinutes int  `json:"record_duration_minutes"`
}
