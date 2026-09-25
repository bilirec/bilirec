package recorder

import (
	"os"
	"path/filepath"

	"github.com/bilirec/bilirec/internal/services/danmaku"
	"github.com/bilirec/bilirec/pkg/ds"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/utils"
)

type recordingFile struct {
	path    string
	stamp   string
	segment int
}

func (r *Service) ensureDiskSpace(l logger.Logger, p internalStartParams) error {
	if r.cfg.MinDiskSpaceBytes <= 0 {
		return nil
	}
	for {
		usage, err := utils.GetDiskSpace(r.cfg.OutputDir)
		if err != nil {
			l.Warnf("cannot check disk space: %v", err)
			return nil
		}
		if !isInsufficientDiskSpace(usage.Free, r.cfg.MinDiskSpaceBytes) {
			return nil
		}
		if !p.opts.deleteOldestOnLowDisk {
			return ErrInsufficientDiskSpace
		}
		oldest, ok := r.pickOldestDeletableRecording(p)
		if !ok {
			return ErrInsufficientDiskSpace
		}
		base := filepath.Base(oldest.path)
		if r.writingFiles.Contains(base) {
			continue
		}
		if err := os.Remove(oldest.path); err != nil {
			l.Warnf("删除最旧录像失败 room=%d path=%s err=%v", p.roomId, oldest.path, err)
			return ErrInsufficientDiskSpace
		}
		for _, ext := range []string{".jsonl", ".xml"} {
			_ = utils.RemoveIfExists(danmaku.PathForVideo(oldest.path, ext))
		}
		l.Infof("磁盘空间不足，已删除房间 %d 最旧录像：%s", p.roomId, oldest.path)
	}
}

func (r *Service) pickOldestDeletableRecording(p internalStartParams) (recordingFile, bool) {
	protected := r.protectedBasenames(p)
	return oldestRecordingFile(r.cfg.OutputDir, p.roomId, protected)
}

func (r *Service) protectedBasenames(p internalStartParams) ds.Set[string] {
	skip := ds.NewSet[string]()
	for _, name := range r.writingFiles.ToSlice() {
		skip.Add(name)
	}
	if p.mode == startModeRecovery && p.session != nil {
		if cur := p.session.OutputPath(); cur != "" {
			skip.Add(filepath.Base(cur))
		}
	}
	return skip
}

func oldestRecordingFile(outputDir string, roomID int, skip ds.Set[string]) (recordingFile, bool) {
	var best recordingFile
	found := false
	for _, dir := range utils.RoomRecordingDirs(outputDir, roomID) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if skip.Contains(name) {
				continue
			}
			stamp, segment, ok := utils.ParseRecordingSegmentFilename(name)
			if !ok {
				continue
			}
			path := filepath.Join(dir, name)
			candidate := recordingFile{path: path, stamp: stamp, segment: segment}
			if !found || recordingFilenameOlder(candidate, best) {
				best = candidate
				found = true
			}
		}
	}
	return best, found
}

func recordingFilenameOlder(a, b recordingFile) bool {
	if a.stamp != b.stamp {
		return a.stamp < b.stamp
	}
	if a.segment != b.segment {
		return a.segment < b.segment
	}
	return a.path < b.path
}
