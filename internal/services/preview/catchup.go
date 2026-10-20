package preview

import (
	"bufio"
	"bytes"
	"os"
)

func (s *Service) Catchup(sidecarPath string) ([][]byte, error) {
	if s == nil {
		return nil, nil
	}
	return readJSONLLines(sidecarPath)
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
