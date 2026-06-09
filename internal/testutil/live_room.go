package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	broadcastsEndpoint     = "https://workers.vrp.moe/laplace/ranking?type=danmakus"
	envLiveRoomID          = "BILIBILI_TEST_ROOM_ID"
	envLiveRoomIDs         = "BILIBILI_TEST_ROOM_IDS"
	broadcastsFetchTimeout = 10 * time.Second
)

type broadcastEntry struct {
	RoomID int `json:"roomid"`
}

var (
	liveRoomOnce sync.Once
	liveRoomIDs  []int
	liveRoomErr  error
)

// LiveRoomID returns one live room ID, preferring environment overrides when set.
// It skips the test if no live rooms can be obtained from the broadcast API.
func LiveRoomID(tb testing.TB) int {
	tb.Helper()
	ids := LiveRoomIDs(tb, 1)
	if len(ids) == 0 {
		tb.Skip("no live room ids available")
	}
	tb.Logf("using live room id: %d", ids[0])
	return ids[0]
}

// LiveRoomIDs returns n live room IDs, shuffling the cached broadcast pool.
// If environment overrides are set, they are used first.
func LiveRoomIDs(tb testing.TB, n int) []int {
	tb.Helper()
	if n <= 0 {
		return nil
	}

	if ids, ok := parseEnvRoomIDs(tb, n); ok {
		return ids
	}

	pool := cachedBroadcastRoomIDs(tb)
	if len(pool) == 0 {
		tb.Skip("no live room ids available")
	}

	shuffled := append([]int(nil), pool...)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	r.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	ids := make([]int, 0, n)
	for len(ids) < n {
		ids = append(ids, shuffled[len(ids)%len(shuffled)])
	}
	tb.Logf("will use these room ids: %v", ids)
	return ids
}

func parseEnvRoomIDs(tb testing.TB, n int) ([]int, bool) {
	tb.Helper()
	if raw := strings.TrimSpace(os.Getenv(envLiveRoomIDs)); raw != "" {
		ids := parseRoomIDList(tb, raw)
		if len(ids) == 0 {
			tb.Fatalf("%s did not contain any valid room ids", envLiveRoomIDs)
		}
		return expandRoomIDs(ids, n), true
	}

	if n == 1 {
		if raw := strings.TrimSpace(os.Getenv(envLiveRoomID)); raw != "" {
			id := parseRoomID(tb, raw, envLiveRoomID)
			return []int{id}, true
		}
	}

	return nil, false
}

func expandRoomIDs(ids []int, n int) []int {
	if n <= len(ids) {
		return append([]int(nil), ids[:n]...)
	}

	result := make([]int, 0, n)
	for len(result) < n {
		result = append(result, ids[len(result)%len(ids)])
	}
	return result
}

func parseRoomIDList(tb testing.TB, raw string) []int {
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
		ids = append(ids, parseRoomID(tb, part, envLiveRoomIDs))
	}
	return ids
}

func parseRoomID(tb testing.TB, raw string, source string) int {
	tb.Helper()
	id, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || id <= 0 {
		tb.Fatalf("invalid room id in %s: %q", source, raw)
	}
	return id
}

func cachedBroadcastRoomIDs(tb testing.TB) []int {
	tb.Helper()
	liveRoomOnce.Do(func() {
		liveRoomIDs, liveRoomErr = fetchBroadcastRoomIDs()
	})
	if liveRoomErr != nil {
		tb.Skipf("failed to fetch live room ids: %v", liveRoomErr)
	}
	return append([]int(nil), liveRoomIDs...)
}

func fetchBroadcastRoomIDs() ([]int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), broadcastsFetchTimeout)
	defer cancel()

	payload, err := fetchBroadcastPage(ctx)
	if err != nil {
		return nil, err
	}

	seen := make(map[int]struct{}, len(payload))
	ids := make([]int, 0, len(payload))
	for _, item := range payload {
		if item.RoomID <= 0 {
			continue
		}
		if _, ok := seen[item.RoomID]; ok {
			continue
		}
		seen[item.RoomID] = struct{}{}
		ids = append(ids, item.RoomID)
	}

	if len(ids) == 0 {
		return nil, fmt.Errorf("broadcast API 未返回有效的直播间 ID")
	}
	return ids, nil
}

func fetchBroadcastPage(ctx context.Context) ([]broadcastEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, broadcastsEndpoint, nil)
	if err != nil {
		return nil, err
	}

	// add user agent
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; bilirec/1.0; +https://github.com/bilirec/bilirec)")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("broadcast API 返回状态 %s", resp.Status)
	}

	var payload []broadcastEntry
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}
