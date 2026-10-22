package preview

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// How far back from the latest jsonl timestamp to replay on preview join.
// Long enough for a hanging Super Chat; short enough not to dump hours of gifts.
const catchupWindowSec = 60

type catchupLine struct {
	Type string  `json:"type"`
	Ts   float64 `json:"ts"`
}

func (s *Service) Catchup(roomID int) ([][]byte, error) {
	s.mu.Lock()
	r := s.rooms[roomID]
	if r == nil || r.path == "" {
		s.mu.Unlock()
		return nil, nil
	}
	videoPath := r.path
	s.mu.Unlock()

	sidecar := strings.TrimSuffix(videoPath, filepath.Ext(videoPath)) + ".jsonl"
	lines, err := readJSONLLines(sidecar)
	if err != nil {
		return lines, err
	}
	return filterCatchup(lines), nil
}

func readJSONLLines(path string) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var lines [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		lines = append(lines, append([]byte(nil), line...))
	}
	if err := sc.Err(); err != nil {
		return lines, err
	}
	if st.Size() > 0 {
		var last [1]byte
		if _, err := f.ReadAt(last[:], st.Size()-1); err == nil && last[0] != '\n' && len(lines) > 0 {
			lines = lines[:len(lines)-1]
		}
	}
	return lines, nil
}

func filterCatchup(lines [][]byte) [][]byte {
	if len(lines) == 0 {
		return lines
	}
	parsed := make([]catchupLine, len(lines))
	maxTs := 0.0
	for i, line := range lines {
		_ = json.Unmarshal(line, &parsed[i])
		if parsed[i].Ts > maxTs {
			maxTs = parsed[i].Ts
		}
	}
	cutoff := maxTs - catchupWindowSec
	kept := make([][]byte, 0, len(lines))
	for i, line := range lines {
		if parsed[i].Type == "meta" || parsed[i].Ts >= cutoff {
			kept = append(kept, line)
		}
	}
	return kept
}

func rebasePreviewTs(line []byte, originSec float64) []byte {
	if originSec <= 0 || len(line) == 0 {
		return line
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(line, &obj); err != nil {
		return line
	}
	raw, ok := obj["ts"]
	if !ok {
		return line
	}
	var ts float64
	if err := json.Unmarshal(raw, &ts); err != nil {
		return line
	}
	shifted, err := json.Marshal(ts - originSec)
	if err != nil {
		return line
	}
	obj["ts"] = shifted
	out, err := json.Marshal(obj)
	if err != nil {
		return line
	}
	return out
}
