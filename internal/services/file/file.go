package file

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/path"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

var logger = logrus.WithField("service", "file")

var ErrIsDirectory = fmt.Errorf("路径是目录")

type Service struct {
	cfg *config.Config
	ctx context.Context

	path *path.Service

	serveCacheMu                sync.Mutex
	serveCache                  map[string]*serveCacheSession
	dropPageCacheFn             func(string) error
	serveCacheIdleDelayOverride time.Duration
}

type Tree struct {
	Name        string `json:"name"`
	IsDir       bool   `json:"is_dir"`
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	IsRecording bool   `json:"is_recording,omitempty"`
}

type PagedTree struct {
	Total int    `json:"total"`
	Items []Tree `json:"items"`
}

type ListOptions struct {
	Filter func(fs.DirEntry) bool // nil = no filter
	Search string                 // empty = no search
	Offset int
	Limit  int // 0 = all
}

func NewService(ls fx.Lifecycle, cfg *config.Config, pathSvc *path.Service) *Service {
	ctx, cancel := context.WithCancel(context.Background())

	s := &Service{
		cfg:        cfg,
		ctx:        ctx,
		path:       pathSvc,
		serveCache: make(map[string]*serveCacheSession),
	}

	ls.Append(fx.StopHook(func() {
		cancel()
		s.stopServeCacheRelease()
	}))
	return s
}

func (s *Service) ListTree(path string) ([]Tree, error) {
	return s.ListTreeWithFilter(path, func(f fs.DirEntry) bool {
		return !strings.HasSuffix(f.Name(), ".tmp") // ignore .tmp files
	})
}

func (s *Service) ListTreeWithFilter(path string, filter func(fs.DirEntry) bool) ([]Tree, error) {
	fullPath, err := s.path.ValidatePath(path)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, err
	}

	relativePath, err := s.path.GetRelativePath(fullPath)
	if err != nil {
		return nil, err
	}

	files := make([]Tree, 0)
	for _, entry := range entries {
		if filter(entry) {
			entryPath := filepath.Join(relativePath, entry.Name())
			files = append(files, Tree{
				Name:  entry.Name(),
				IsDir: entry.IsDir(),
				Path:  entryPath,
				Size: func() int64 {
					if entry.IsDir() {
						return 0
					} else if info, err := entry.Info(); err == nil {
						return info.Size()
					}
					return 0
				}(),
			})
		}
	}
	return files, nil
}

func (s *Service) ListTreeWithOptions(path string, opts ListOptions) (*PagedTree, error) {
	fullPath, err := s.path.ValidatePath(path)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, err
	}

	relativePath, err := s.path.GetRelativePath(fullPath)
	if err != nil {
		return nil, err
	}

	// Apply filter + search to collect matching entries (cheap: no stat)
	filtered := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if opts.Filter != nil && !opts.Filter(entry) {
			continue
		}
		if opts.Search != "" && !strings.Contains(strings.ToLower(entry.Name()), strings.ToLower(opts.Search)) {
			continue
		}
		filtered = append(filtered, entry)
	}

	total := len(filtered)

	// Determine the page slice
	start := min(opts.Offset, total)
	end := total
	if opts.Limit > 0 {
		end = min(start + opts.Limit, total)
	}
	page := filtered[start:end]

	// Build Tree items — only call entry.Info() (stat) for files in this page
	items := make([]Tree, 0, len(page))
	for _, entry := range page {
		entryPath := filepath.Join(relativePath, entry.Name())
		items = append(items, Tree{
			Name:  entry.Name(),
			IsDir: entry.IsDir(),
			Path:  entryPath,
			Size: func() int64 {
				if entry.IsDir() {
					return 0
				} else if info, err := entry.Info(); err == nil {
					return info.Size()
				}
				return 0
			}(),
		})
	}

	return &PagedTree{
		Total: total,
		Items: items,
	}, nil
}

func (s *Service) GetFileStream(path string) (io.ReadCloser, os.FileInfo, error) {
	fullPath, err := s.path.ValidatePath(path)
	if err != nil {
		return nil, nil, err
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		return nil, nil, ErrIsDirectory
	}

	if f, err := os.Open(fullPath); err != nil {
		return nil, nil, err
	} else {
		return f, info, nil
	}
}

func (s *Service) DeleteDirectory(path string) error {
	fullPath, err := s.path.ValidatePath(path)
	if err != nil {
		return err
	}
	return os.RemoveAll(fullPath)
}

func (s *Service) DeleteFiles(paths ...string) error {
	var fullPaths []string
	for _, path := range paths {
		fullPath, err := s.path.ValidatePath(path)
		if err != nil {
			return err
		}
		fullPaths = append(fullPaths, fullPath)
	}
	for _, fullPath := range fullPaths {
		if err := os.Remove(fullPath); err != nil {
			return err
		}
	}
	return nil
}
