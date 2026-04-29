package convert

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/eric2788/bilirec/pkg/db"
	"github.com/eric2788/bilirec/pkg/pool"
	"github.com/eric2788/bilirec/utils"
	"github.com/puzpuzpuz/xsync/v4"
	"github.com/sirupsen/logrus"
	"go.etcd.io/bbolt"
)

const (
	ProviderFFmpeg Provider = "ffmpeg"

	ffmpegBucket = "Queue_FFmpeg"
)

type ffmpegConvertManager struct {
	bucket     *db.Bucket
	logger     *logrus.Entry
	serializer *pool.Serializer
	getActives GetActiveRecordings

	processing *xsync.Map[string, context.CancelFunc]
}

func newFFmpegConvertManager(getActives GetActiveRecordings) ConvertManager {
	return &ffmpegConvertManager{
		logger:     logger.WithField("manager", "ffmpeg"),
		serializer: pool.NewSerializer(),
		getActives: getActives,
		processing: xsync.NewMap[string, context.CancelFunc](),
	}
}

func (f *ffmpegConvertManager) StartWorker(ctx context.Context, db *db.Client) error {
	if !utils.FFmpegAvailable() {
		return ErrFFmpegNotInstalled
	} else if bucket, err := db.Bucket(ffmpegBucket); err != nil {
		return err
	} else {
		f.bucket = bucket
	}
	go f.runTaskPeriodically(ctx)
	return nil
}

func (f *ffmpegConvertManager) Enqueue(inputPath, outputPath, format string, deleteSource bool) (*TaskQueue, error) {
	uuid, err := utils.NewUUIDv4()
	if err != nil {
		return nil, err
	}
	queue := &TaskQueue{
		Provider:     ProviderFFmpeg,
		TaskID:       uuid,
		InputPath:    inputPath,
		OutputPath:   outputPath,
		InputFormat:  utils.GetPathFormat(inputPath),
		OutputFormat: format,
		DeleteSource: deleteSource,
	}
	data, err := f.serializer.Serialize(queue)
	if err != nil {
		return nil, err
	}
	err = f.bucket.Put([]byte(uuid), data)
	return queue, err
}

func (f *ffmpegConvertManager) Cancel(taskID string) error {
	if cancel, ok := f.processing.LoadAndDelete(taskID); ok {
		cancel()
	}
	return f.bucket.Delete([]byte(taskID))
}

func (f *ffmpegConvertManager) ListInProgress() ([]*TaskQueue, error) {
	var queues []*TaskQueue
	err := f.bucket.ForEach(func(k, v []byte) error {
		var queue TaskQueue
		if err := f.serializer.Deserialize(v, &queue); err != nil {
			return fmt.Errorf("deserialize task %s: %w", string(k), err)
		}
		queues = append(queues, &queue)
		return nil
	})
	return queues, err
}

func (f *ffmpegConvertManager) runTaskPeriodically(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			actives := f.getActives()
			if actives > 0 {
				f.logger.Debugf("active recordings detected (%d), skipping ffmpeg tasks", actives)
				continue
			}
			var queue *TaskQueue
			if err := f.bucket.View(func(bucket *bbolt.Bucket) error {
				k, v := bucket.Cursor().First()
				if k == nil {
					return nil
				}
				queue = &TaskQueue{}
				if err := f.serializer.Deserialize(v, queue); err != nil {
					return fmt.Errorf("deserialize task %s: %w", string(k), err)
				}
				return nil
			}); err != nil {
				f.logger.Errorf("reading ffmpeg queue task failed: %v", err)
				continue
			} else if queue == nil {
				continue
			}

			deleteBucket := func() error {
				return utils.WithRetry(3, f.logger, "delete bucket", func() error {
					return f.bucket.Delete([]byte(queue.TaskID))
				})
			}

			taskLog := f.logger.WithField("task_id", queue.TaskID)

			if !utils.IsFileExists(queue.InputPath) {
				taskLog.Warnf("input file %s no longer exists, cancelling task", queue.InputPath)
				if err := deleteBucket(); err != nil {
					taskLog.Errorf("failed to remove ffmpeg task from queue: %v", err)
				}
				continue
			}

			taskLog.Infof("processing ffmpeg task input=%s output=%s", queue.InputPath, queue.OutputPath)

			if err := f.processTask(ctx, queue, taskLog); err != nil {
				taskLog.Errorf("ffmpeg task failed: %v", err)
				continue
			}

			if err := deleteBucket(); err != nil {
				taskLog.Errorf("failed to remove ffmpeg task from queue: %v", err)
				continue
			}

			taskLog.Info("completed and removed from queue")
		case <-ctx.Done():
			return
		}
	}
}

func (f *ffmpegConvertManager) processTask(ctx context.Context, queue *TaskQueue, taskLog *logrus.Entry) error {

	if utils.IsFileExists(queue.OutputPath) {
		taskLog.Warnf("output file %s already exists, skipping conversion", queue.OutputPath)
		return nil
	}

	processCtx, cancel := context.WithCancel(ctx)
	f.processing.Store(queue.TaskID, cancel)
	defer func() {
		if cancel, ok := f.processing.LoadAndDelete(queue.TaskID); ok {
			cancel()
		}
	}()

	cmd := exec.CommandContext(processCtx,
		"ffmpeg",
		"-hide_banner",
		"-i",
		queue.InputPath,
		"-map",
		"0",
		"-map_metadata",
		"0",
		"-movflags",
		"+faststart",
		"-c",
		"copy",
		queue.OutputPath,
	)

	cmd.Stdout = taskLog.Writer()
	cmd.Stderr = taskLog.Writer()

	if err := cmd.Run(); err != nil {
		return err
	} else if err := validateOutputFileSize(queue.InputPath, queue.OutputPath); err != nil {
		return err
	} else if !queue.DeleteSource || queue.InputPath == queue.OutputPath {
		return nil
	}

	return utils.WithRetry(3, taskLog, "delete source file", func() error {
		if !utils.IsFileExists(queue.InputPath) {
			taskLog.Debugf("source file %s does not exist, skipping delete", queue.InputPath)
			return nil
		}
		return os.Remove(queue.InputPath)
	})
}
