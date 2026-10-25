package recorder

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/bilirec/bilirec/pkg/ds"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/utils"
)

type recordingFile struct {
	path    string
	stamp   string
	segment int
	size    int64
}

func (r *Service) ensureDiskSpace(l logger.Logger, p internalStartParams) error {
	if r.cfg.MinDiskSpaceBytes <= 0 {
		return nil
	}
	const maxPasses = 2
	for pass := 0; pass < maxPasses; pass++ {
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
		need := int64(r.cfg.MinDiskSpaceBytes) - int64(usage.Free)
		files := r.listDeletableRecordings(p)
		freed, deleted := r.deleteUntil(l, p.roomId, files, need)
		if deleted == 0 {
			return ErrInsufficientDiskSpace
		}
		if freed >= need {
			break
		}
	}
	usage, err := utils.GetDiskSpace(r.cfg.OutputDir)
	if err != nil {
		l.Warnf("cannot check disk space: %v", err)
		return nil
	}
	if isInsufficientDiskSpace(usage.Free, r.cfg.MinDiskSpaceBytes) {
		return ErrInsufficientDiskSpace
	}
	return nil
}

func (r *Service) listDeletableRecordings(p internalStartParams) []recordingFile {
	protected := r.protectedBasenames(p)
	var files []recordingFile
	for _, dir := range utils.RoomRecordingDirs(r.cfg.OutputDir, p.roomId) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if protected.Contains(name) {
				continue
			}
			stamp, segment, ok := utils.ParseRecordingSegmentFilename(name)
			if !ok {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			files = append(files, recordingFile{
				path:    filepath.Join(dir, name),
				stamp:   stamp,
				segment: segment,
				size:    info.Size(),
			})
		}
	}
	sort.SliceStable(files, func(i, j int) bool {
		return recordingFilenameOlder(files[i], files[j])
	})
	return files
}

func (r *Service) deleteUntil(l logger.Logger, roomID int, files []recordingFile, need int64) (freed int64, deleted int) {
	for _, f := range files {
		if freed >= need {
			break
		}
		base := filepath.Base(f.path)
		if r.writingFiles.Contains(base) {
			continue
		}
		if err := os.Remove(f.path); err != nil {
			l.Warnf("删除最旧录像失败 room=%d path=%s err=%v", roomID, f.path, err)
			continue
		}
		for _, ext := range []string{".jsonl", ".xml", ".vtt"} {
			_ = utils.RemoveIfExists(utils.SidecarPathForVideo(f.path, ext))
		}
		l.Infof("磁盘空间不足，已删除房间 %d 最旧录像：%s", roomID, f.path)
		freed += f.size
		deleted++
	}
	return freed, deleted
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

func recordingFilenameOlder(a, b recordingFile) bool {
	if a.stamp != b.stamp {
		return a.stamp < b.stamp
	}
	if a.segment != b.segment {
		return a.segment < b.segment
	}
	return a.path < b.path
}
