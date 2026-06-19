package convert

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/db"
	"github.com/bilirec/bilirec/pkg/ffmpeg"
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/bilirec/bilirec/utils"
	"github.com/puzpuzpuz/xsync/v4"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"
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
	deleter    *sourceDeleter

	processing *xsync.Map[string, context.CancelFunc]
	concurrent *semaphore.Weighted
	cooldowns  *xsync.Map[string, time.Time]
}

func newFFmpegConvertManager(getActives GetActiveRecordings, deleter *sourceDeleter) ConvertManager {
	return &ffmpegConvertManager{
		logger:     logger.WithField("manager", "ffmpeg"),
		serializer: pool.NewSerializer(),
		getActives: getActives,
		deleter:    deleter,
		processing: xsync.NewMap[string, context.CancelFunc](),
		concurrent: semaphore.NewWeighted(int64(config.ReadOnly.FFmpegMaxConcurrentTasks())),
		cooldowns:  xsync.NewMap[string, time.Time](),
	}
}

func (f *ffmpegConvertManager) StartWorker(ctx context.Context, wg *sync.WaitGroup, db *db.Client) error {
	if !ffmpeg.Available() {
		return ErrFFmpegNotInstalled
	} else if bucket, err := db.Bucket(ffmpegBucket); err != nil {
		return err
	} else {
		f.bucket = bucket
	}
	wg.Add(1)
	go f.runTaskPeriodically(ctx, wg)
	return nil
}

func (f *ffmpegConvertManager) Enqueue(inputPath, outputPath, format string, deleteSource bool) (*TaskQueue, error) {
	uuid, err := utils.NewUUIDv4()
	if err != nil {
		return nil, err
	}
	queue := &TaskQueue{
		Provider:      ProviderFFmpeg,
		TaskID:        uuid,
		InputPath:     inputPath,
		InputFileSize: fileSize(inputPath),
		OutputPath:    outputPath,
		InputFormat:   utils.GetPathFormat(inputPath),
		OutputFormat:  format,
		DeleteSource:  deleteSource,
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
			return fmt.Errorf("反序列化任务 %s 失败：%w", string(k), err)
		}
		queues = append(queues, &queue)
		return nil
	})
	return queues, err
}

func (f *ffmpegConvertManager) InProgressSize() int {
	count, _ := f.bucket.Count()
	return count
}

func (f *ffmpegConvertManager) runTaskPeriodically(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(time.Duration(config.ReadOnly.FFmpegCheckIntervalSecs()) * time.Second)
	defer ticker.Stop()

	var swg sync.WaitGroup
	defer swg.Wait()

	for {
		select {
		case <-ticker.C:
			actives := f.getActives()
			allowDuringRecording := config.ReadOnly.FFmpegAllowDuringRecording()
			allowDuringRecordingMaxActiveRecordings := config.ReadOnly.FFmpegAllowDuringRecordingMaxActiveRecordings()
			if actives > 0 && !allowConvertDuringRecording(actives, allowDuringRecording, allowDuringRecordingMaxActiveRecordings) {
				if !allowDuringRecording {
					f.logger.Debugf("active recordings detected (%d), skipping ffmpeg tasks", actives)
				} else {
					f.logger.Debugf("active recordings detected (%d), require <= %d to run ffmpeg during recording, skipping tasks", actives, allowDuringRecordingMaxActiveRecordings)
				}
				continue
			}

			list, err := f.ListInProgress()
			if err != nil {
				f.logger.Errorf("列出 ffmpeg 进行中的任务失败：%v", err)
				continue
			} else if len(list) == 0 {
				continue
			}

			for _, queue := range list {
				taskLog := f.logger.WithField("task_id", queue.TaskID)

				if _, processing := f.processing.Load(queue.TaskID); processing {
					taskLog.Debug("task is already being processed, skip this cycle")
					continue
				} else if cooldown, onCooldown := f.cooldowns.Load(queue.TaskID); onCooldown {
					if time.Now().Before(cooldown) {
						taskLog.Debugf("task is on cooldown until %v, skip this cycle", cooldown.Format(time.RFC3339))
						continue
					}
					f.cooldowns.Delete(queue.TaskID)
				}

				if !utils.IsFileExists(queue.InputPath) {
					taskLog.Warnf("输入文件 %s 已不存在，正在取消任务", queue.InputPath)
					if err := f.deleteTaskFromQueue(queue.TaskID); err != nil {
						taskLog.Errorf("从队列移除 ffmpeg 任务失败：%v", err)
					}
					continue
				}

				if !f.concurrent.TryAcquire(1) {
					taskLog.Debug("ffmpeg concurrency limit reached, defer remaining tasks to next cycle")
					break
				}

				processCtx, cancel := context.WithCancel(ctx)
				f.processing.Store(queue.TaskID, cancel)

				taskLog.Infof("正在处理 ffmpeg 任务 input=%s output=%s", queue.InputPath, queue.OutputPath)
				swg.Go(func() {
					f.asyncProcessTask(processCtx, queue, taskLog)
				})
			}
		case <-ctx.Done():
			return
		}
	}
}

func (f *ffmpegConvertManager) deleteTaskFromQueue(taskID string) error {
	return utils.WithRetry(3, f.logger, "delete bucket", func() error {
		return f.bucket.Delete([]byte(taskID))
	})
}

func (f *ffmpegConvertManager) asyncProcessTask(ctx context.Context, queue *TaskQueue, taskLog *logrus.Entry) {
	defer func() {
		if cancel, ok := f.processing.LoadAndDelete(queue.TaskID); ok {
			cancel()
		}
	}()

	defer f.concurrent.Release(1)

	if err := f.processTask(ctx, queue, taskLog); err != nil {
		taskLog.Errorf("ffmpeg 任务失败：%v", err)
		// delay the tasks to interval + 30s to avoid multiple tasks failing at the same time and retrying immediately
		delay := time.Duration(config.ReadOnly.FFmpegCheckIntervalSecs())*time.Second + 30*time.Second
		delayTime := time.Now().Add(delay)
		f.cooldowns.Store(queue.TaskID, delayTime)
		taskLog.Warnf("任务已延后至 %v", delayTime.Format(time.RFC3339))
		return
	}

	if err := f.deleteTaskFromQueue(queue.TaskID); err != nil {
		taskLog.Errorf("从队列移除 ffmpeg 任务失败：%v", err)
		return
	}

	f.deleter.Schedule(queue, taskLog)
	taskLog.Info("任务已完成并从队列移除")
}

func (f *ffmpegConvertManager) processTask(ctx context.Context, queue *TaskQueue, taskLog *logrus.Entry) error {

	if !utils.IsFileExists(queue.InputPath) {
		taskLog.Warnf("输入文件 %s 已不存在，跳过转码", queue.InputPath)
		return nil
	} else if utils.IsFileExists(queue.OutputPath) {
		taskLog.Warnf("输出文件 %s 已存在，跳过转码", queue.OutputPath)
		return nil
	}

	if err := ffmpeg.Run(ctx, taskLog,
		"-hide_banner",
		"-i",
		queue.InputPath,
		"-map",
		"0:v?",
		"-map",
		"0:a?",
		"-movflags",
		"+faststart",
		"-c",
		"copy",
		queue.OutputPath,
	); err != nil {
		return err
	} else if err := ValidateOutputFileSize(queue.InputPath, queue.OutputPath); err != nil {
		return err
	}
	return nil
}
