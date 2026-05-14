package utils

// ParseRoomIDs parses room IDs from request body first, then falls back to query params.
// Supported body payloads:
// - [1,2,3]
// - {"roomIDs":[1,2,3]}
// - {"roomIDs":"1,2,3"}
func ParseRoomIDs(queryValue string, body []byte) ([]int, error) {
	return ParseIntList("roomIDs", queryValue, body, "roomIDs", "room_ids", "roomIds")
}
