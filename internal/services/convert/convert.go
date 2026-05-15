package convert

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/eric2788/bilirec/internal/modules/config"
	"github.com/eric2788/bilirec/internal/services/path"
	"github.com/eric2788/bilirec/pkg/cloudconvert"
	"github.com/eric2788/bilirec/pkg/db"
	"github.com/eric2788/bilirec/utils"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

var logger = logrus.WithField("service", "convert")

var (
	ErrTaskNotFound              = errors.New("convert task not found")
	ErrNoConvertManager          = errors.New("no convert manager available")
	ErrFFmpegNotInstalled        = errors.New("ffmpeg is not installed or not found in PATH")
	ErrCloudConvertNotConfigured = errors.New("cloudconvert client is not initialized")
	ErrInvalidPublicBaseURL      = errors.New("invalid PUBLIC_BASE_URL for cloudconvert presigned URL")
)

type Service struct {
	cloudthreshold int64
	managers       map[string]ConvertManager
	ctx            context.Context
	db             *db.Client
}

func NewService(ls fx.Lifecycle, cfg *config.Config, pathSvc *path.Service) *Service {
	ctx, cancel := context.WithCancel(context.Background())

	svc := &Service{
		cloudthreshold: cfg.CloudConvertThreshold,
		managers:       make(map[string]ConvertManager),
		ctx:            ctx,
	}

	if cfg.CloudConvertApiKey != "" {
		svc.managers["cloudconvert"] = newCloudConvertManager(
			cloudconvert.NewClient(
				ctx,
				cfg.CloudConvertApiKey,
				cloudconvert.WithUploadBufferSize(config.ReadOnly.UploadBufferSize()),
			),
			pathSvc,
		)
	} else {
		logger.Info("cloud convert api key not provided, cloud convert disabled")
	}

	ls.Append(fx.StartStopHook(
		func() error {
			// use bbolt for offline storage
			db, err := db.Open(cfg.DatabaseDir + string(os.PathSeparator) + "queues.db")
			if err != nil {
				return err
			}
			for _, manager := range svc.managers {
				if err := manager.StartWorker(ctx, db); err != nil {
					return fmt.Errorf("failed to start convert manager: %v", err)
				}
			}
			svc.db = db
			return nil
		},
		func() error {
			cancel()
			return svc.db.Close()
		},
	))
	return svc
}

func (s *Service) Enqueue(path, format string, deleteSource bool) (*TaskQueue, error) {
	if err := s.checkAvailableManagers(); err != nil {
		return nil, err
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	outputFormat := utils.ChangePathFormat(path, format)
	var manager ConvertManager
	if s.shoulduseCloudConvert(fileInfo.Size()) {
		manager = s.managers["cloudconvert"]
	} else {
		manager = s.managers["ffmpeg"]
	}
	return manager.Enqueue(path, outputFormat, format, deleteSource)
}

// IsInQueue checks if the given full path is already in the convert queue.
// It must be full path
func (s *Service) IsInQueue(fullPath string) (bool, error) {
	queues, err := s.ListInProgress()
	if err != nil {
		return false, err
	}
	for _, q := range queues {
		if q.InputPath == fullPath {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) Cancel(taskID string) error {
	if err := s.checkAvailableManagers(); err != nil {
		return err
	}
	for _, manager := range s.managers {
		if err := manager.Cancel(taskID); err == nil {
			return nil
		} else if err != ErrTaskNotFound {
			return err
		}
	}
	return ErrTaskNotFound
}

func (s *Service) ListInProgress() ([]*TaskQueue, error) {
	if err := s.checkAvailableManagers(); err != nil {
		return nil, err
	}
	allQueues := make([]*TaskQueue, 0)
	for _, manager := range s.managers {
		queues, err := manager.ListInProgress()
		if err != nil {
			return nil, err
		}
		allQueues = append(allQueues, queues...)
	}
	return allQueues, nil
}

func (s *Service) SetActiveRecordingsGetter(getter GetActiveRecordings) {
	if _, ok := s.managers["ffmpeg"]; ok {
		return
	} else if utils.FFmpegAvailable() {
		s.managers["ffmpeg"] = newFFmpegConvertManager(getter)
	} else {
		logger.Warn("ffmpeg not available, ffmpeg convert manager not initialized")
	}
}

func (s *Service) shoulduseCloudConvert(fileSize int64) bool {
	_, cloudEnabled := s.managers["cloudconvert"]
	return cloudEnabled && s.cloudthreshold >= 0 && fileSize >= s.cloudthreshold
}

func (s *Service) checkAvailableManagers() error {
	if len(s.managers) == 0 {
		return ErrNoConvertManager
	}
	return nil
}

func fileSize(path string) *int64 {
	info, err := os.Stat(path)
	if err != nil {
		logger.Warnf("failed to get file info for path %s: %v", path, err)
		return nil
	}
	return utils.Ptr(info.Size())
}
