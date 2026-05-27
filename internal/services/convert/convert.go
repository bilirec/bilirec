package convert

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/path"
	"github.com/bilirec/bilirec/pkg/cloudconvert"
	"github.com/bilirec/bilirec/pkg/db"
	"github.com/bilirec/bilirec/utils"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

var logger = logrus.WithField("service", "convert")

var (
	ErrTaskNotFound              = errors.New("转码任务未找到")
	ErrNoConvertManager          = errors.New("没有可用的转码管理器")
	ErrFFmpegNotInstalled        = errors.New("ffmpeg 未安装或未在 PATH 中找到")
	ErrCloudConvertNotConfigured = errors.New("cloudconvert 客户端未初始化")
	ErrInvalidPublicBaseURL      = errors.New("用于 cloudconvert 预签名 URL 的 PUBLIC_BASE_URL 无效")
)

type Service struct {
	cloudthreshold int64
	managers       map[string]ConvertManager
	ctx            context.Context
	db             *db.Client

	noConvertIfInvalid bool
	wg                 sync.WaitGroup
}

func NewService(ls fx.Lifecycle, cfg *config.Config, pathSvc *path.Service) *Service {
	ctx, cancel := context.WithCancel(context.Background())

	svc := &Service{
		cloudthreshold:     cfg.CloudConvertThreshold,
		managers:           make(map[string]ConvertManager),
		ctx:                ctx,
		noConvertIfInvalid: cfg.NoConvertIfInvalid,
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
		logger.Info("未提供 CloudConvert API Key，CloudConvert 已禁用")
	}

	stop := func() error {
		cancel()
		svc.wg.Wait()
		return svc.db.Close()
	}

	ls.Append(fx.StartStopHook(
		func() error {
			// use bbolt for offline storage
			db, err := db.Open(cfg.DatabaseDir + string(os.PathSeparator) + "queues.db")
			if err != nil {
				return err
			}
			svc.db = db
			for _, manager := range svc.managers {
				if err := manager.StartWorker(ctx, &svc.wg, db); err != nil {
					if err := stop(); err != nil {
						logger.Warnf("回滚失败：%v", err)
					}
					return fmt.Errorf("启动转码管理器失败：%v", err)
				}
			}
			return nil
		},
		stop,
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
	if pass, err := s.checkOriginalFile(path); err != nil {
		if !pass && s.noConvertIfInvalid {
			return nil, err
		}
		logger.Warn(err)
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

func (s *Service) InProgressSize() int {
	if err := s.checkAvailableManagers(); err != nil {
		return 0
	}
	var count int
	for _, manager := range s.managers {
		count += manager.InProgressSize()
	}
	return count
}

func (s *Service) SetActiveRecordingsGetter(getter GetActiveRecordings) {
	if _, ok := s.managers["ffmpeg"]; ok {
		return
	} else if utils.FFmpegAvailable() {
		s.managers["ffmpeg"] = newFFmpegConvertManager(getter)
	} else {
		logger.Warn("ffmpeg 不可用，ffmpeg 转码管理器未初始化")
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
		logger.Warnf("获取路径 %s 的文件信息失败：%v", path, err)
		return nil
	}
	return utils.Ptr(info.Size())
}
