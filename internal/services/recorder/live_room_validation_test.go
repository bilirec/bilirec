package recorder_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/services/room"
	"github.com/bilirec/bilirec/internal/testutil"
)

const (
	recorderLiveValidationMaxRounds = 2
	recorderLiveValidationBackoff   = 1500 * time.Millisecond
	recorderLiveValidationMinPool   = 12
	recorderLiveValidationPerRoom   = 6
)

func resolveLiveTestRoomID(tb testing.TB, roomSvc *room.Service) int {
	tb.Helper()
	rooms := resolveLiveTestRoomIDs(tb, roomSvc, 1)
	if len(rooms) == 0 {
		tb.Skip("no validated live room id available")
	}
	return rooms[0]
}

func resolveLiveTestRoomIDs(tb testing.TB, roomSvc *room.Service, required int) []int {
	tb.Helper()
	if required <= 0 {
		return nil
	}

	candidateCount := max(recorderLiveValidationMinPool, required*recorderLiveValidationPerRoom)
	candidates := uniqueInts(testutil.LiveRoomIDs(tb, candidateCount))
	if len(candidates) == 0 {
		tb.Skip("no candidate live room ids available")
	}

	tb.Logf("live room validation candidates: %v", candidates)
	var attemptErrors []string

	for round := 1; round <= recorderLiveValidationMaxRounds; round++ {
		roomSvc.InvalidateRooms(candidates...)
		infos, err := roomSvc.GetMultipleRoomInfos(candidates...)
		if err != nil {
			attemptErrors = append(attemptErrors, fmt.Sprintf("round %d fetch error: %v", round, err))
			if round < recorderLiveValidationMaxRounds {
				time.Sleep(recorderLiveValidationBackoff)
			}
			continue
		}

		validated := make([]int, 0, required)
		for _, roomID := range candidates {
			info, ok := infos[strconv.Itoa(roomID)]
			if !ok || info == nil {
				attemptErrors = append(attemptErrors, fmt.Sprintf("round %d room %d missing info", round, roomID))
				continue
			}
			if info.LiveStatus == 1 {
				validated = append(validated, roomID)
				if len(validated) == required {
					tb.Logf("validated live room ids (round %d): %v", round, validated)
					return validated
				}
				continue
			}
			attemptErrors = append(attemptErrors, fmt.Sprintf("round %d room %d offline (live_status=%d)", round, roomID, info.LiveStatus))
		}

		if round < recorderLiveValidationMaxRounds {
			time.Sleep(recorderLiveValidationBackoff)
		}
	}

	tb.Skipf("unable to validate %d live room(s); candidates=%v; details=%s", required, candidates, strings.Join(attemptErrors, "\n"))
	return nil
}

func uniqueInts(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[int]struct{}, len(values))
	result := make([]int, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
