package recorder_test

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/services/room"
	"github.com/bilirec/bilirec/internal/testutil"
)

const (
	recorderLiveValidationMaxRetriesAfterUnableValidateAllRooms = 2
	recorderLiveValidationBackoff                               = 1500 * time.Millisecond
	recorderLiveValidationMinPool                               = 12
	recorderLiveValidationPerRoom                               = 6
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

	// BILIBILI_TEST_ROOM_ID is only honored by testutil.LiveRoomIDs when n==1.
	// Recorder validation always requests a larger pool, so check env pins here
	// first to avoid silently falling back to the broadcast ranking API.
	if pinned := pinnedTestRoomIDsFromEnv(tb); len(pinned) > 0 {
		return validateLiveRoomIDs(tb, roomSvc, pinned, required)
	}

	candidateCount := max(recorderLiveValidationMinPool, required*recorderLiveValidationPerRoom)
	candidates := uniqueInts(testutil.LiveRoomIDs(tb, candidateCount))
	if len(candidates) == 0 {
		tb.Skip("no candidate live room ids available")
	}

	return validateLiveRoomIDs(tb, roomSvc, candidates, required)
}

func pinnedTestRoomIDsFromEnv(tb testing.TB) []int {
	tb.Helper()
	if raw := strings.TrimSpace(os.Getenv("BILIBILI_TEST_ROOM_IDS")); raw != "" {
		return uniqueInts(parseTestRoomIDList(tb, raw, "BILIBILI_TEST_ROOM_IDS"))
	}
	if raw := strings.TrimSpace(os.Getenv("BILIBILI_TEST_ROOM_ID")); raw != "" {
		return []int{parseTestRoomID(tb, raw, "BILIBILI_TEST_ROOM_ID")}
	}
	return nil
}

func parseTestRoomIDList(tb testing.TB, raw, source string) []int {
	tb.Helper()
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	})
	ids := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		ids = append(ids, parseTestRoomID(tb, part, source))
	}
	return ids
}

func parseTestRoomID(tb testing.TB, raw, source string) int {
	tb.Helper()
	id, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || id <= 0 {
		tb.Fatalf("invalid room id in %s: %q", source, raw)
	}
	return id
}

func validateLiveRoomIDs(tb testing.TB, roomSvc *room.Service, candidates []int, required int) []int {
	tb.Helper()
	if len(candidates) == 0 {
		tb.Skip("no candidate live room ids available")
	}

	tb.Logf("live room validation candidates: %v", candidates)
	var attemptErrors []string

	maxRounds := recorderLiveValidationMaxRetriesAfterUnableValidateAllRooms + 1
	for round := 1; round <= maxRounds; round++ {
		roomSvc.InvalidateRooms(candidates...)
		infos, err := roomSvc.GetMultipleRoomInfos(candidates...)
		if err != nil {
			attemptErrors = append(attemptErrors, fmt.Sprintf("round %d fetch error: %v", round, err))
			if round < maxRounds {
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

		if round < maxRounds {
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
