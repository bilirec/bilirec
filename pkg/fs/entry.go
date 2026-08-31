package fs

import (
	"encoding/json"
	iofs "io/fs"
	"time"
)

type dirEnt struct {
	name  string
	isDir bool
	size  int64
}

func (d dirEnt) Name() string { return d.name }
func (d dirEnt) IsDir() bool  { return d.isDir }
func (d dirEnt) Type() iofs.FileMode {
	if d.isDir {
		return iofs.ModeDir
	}
	return 0
}
func (d dirEnt) Info() (iofs.FileInfo, error) { return dirInfo{d}, nil }

type dirInfo struct{ dirEnt }

func (d dirInfo) Size() int64 {
	if d.isDir {
		return 0
	}
	return d.size
}
func (d dirInfo) Mode() iofs.FileMode { return d.Type() }
func (d dirInfo) ModTime() time.Time  { return time.Time{} }
func (d dirInfo) Sys() any            { return nil }

type listRow struct {
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
}

func parseListJSON(raw string) ([]iofs.DirEntry, error) {
	if raw == "" {
		return nil, nil
	}
	var rows []listRow
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, err
	}
	out := make([]iofs.DirEntry, 0, len(rows))
	for _, row := range rows {
		if row.Name == "" || row.Name == "." || row.Name == ".." {
			continue
		}
		out = append(out, dirEnt{name: row.Name, isDir: row.IsDir, size: row.Size})
	}
	return out, nil
}

// mergeDirEntries unions MediaStore rows with native readdir results.
// Native directories overwrite MediaStore entries of the same name so room
// folders stay visible even when they have no MediaStore row. Native files
// fill gaps when FUSE listing still works (app-private dirs, desktop).
func mergeDirEntries(native, media []iofs.DirEntry) []iofs.DirEntry {
	byName := make(map[string]iofs.DirEntry, len(native)+len(media))
	order := make([]string, 0, len(native)+len(media))
	add := func(e iofs.DirEntry, overwrite bool) {
		name := e.Name()
		if _, exists := byName[name]; exists {
			if overwrite {
				byName[name] = e
			}
			return
		}
		byName[name] = e
		order = append(order, name)
	}
	for _, e := range media {
		add(e, false)
	}
	for _, e := range native {
		add(e, e.IsDir())
	}
	out := make([]iofs.DirEntry, 0, len(order))
	for _, name := range order {
		out = append(out, byName[name])
	}
	return out
}
