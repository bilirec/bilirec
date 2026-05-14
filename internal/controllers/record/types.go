package record

import "github.com/eric2788/bilirec/internal/services/recorder"

type (
	BatchRoomIDsRequest struct {
		RoomIDs []int `json:"roomIDs"`
	}

	Status struct {
		RoomId int                   `json:"room_id"`
		Status recorder.RecordStatus `json:"status"`
	}

	StopResult struct {
		RoomId  int  `json:"room_id"`
		Success bool `json:"success"`
	}
)
