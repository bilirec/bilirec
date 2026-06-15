package convert

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/path"
	"github.com/bilirec/bilirec/pkg/cloudconvert"
	"github.com/bilirec/bilirec/pkg/db"
	"github.com/bilirec/bilirec/pkg/ds"
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/bilirec/bilirec/pkg/signeddownload"
	"github.com/bilirec/bilirec/utils"
	"github.com/puzpuzpuz/xsync/v4"
	"github.com/sirupsen/logrus"
	"go.etcd.io/bbolt"
	"golang.org/x/sync/semaphore"
)

const (
	ProviderCloudConvert Provider = "cloudconvert"

	cloudConvertBucket = "Queue_CloudConvert"

	importTaskName  = "import-source"
	commandTaskName = "command-convert"
	exportTaskName  = "export-output"
)

type cloudConvertManager struct {
	bucket     *db.Bucket
	logger     *logrus.Entry
	client     *cloudconvert.Client
	serializer *pool.Serializer
	getActives GetActiveRecordings

	processing   ds.AtomicSet[string]
	downloadPool *pool.BytesPool
	concurrent   *semaphore.Weighted

	presignedUrlPool *xsync.Map[string, string] // inputPath -> presignedURL

	pathSvc *path.Service
}

func newCloudConvertManager(client *cloudconvert.Client, pathSvc *path.Service, getActives GetActiveRecordings) ConvertManager {
	return &cloudConvertManager{
		logger:           logger.WithField("manager", "cloudconvert"),
		client:           client,
		serializer:       pool.NewSerializer(),
		getActives:       getActives,
		processing:       ds.NewSyncedSet[string](),
		downloadPool:     pool.NewBytesPool(config.ReadOnly.DownloadBufferSize()),
		concurrent:       semaphore.NewWeighted(int64(config.ReadOnly.CloudConvertMaxConcurrentDownloads())),
		presignedUrlPool: xsync.NewMap[string, string](),
		pathSvc:          pathSvc,
	}
}

func (c *cloudConvertManager) allowDuringRecording(actives int) bool {
	return allowConvertDuringRecording(
		actives,
		config.ReadOnly.CloudConvertAllowDuringRecording(),
		config.ReadOnly.CloudConvertAllowDuringRecordingMaxActiveRecordings(),
	)
}

func (c *cloudConvertManager) logSkipDuringRecording(actives int) {
	if !config.ReadOnly.CloudConvertAllowDuringRecording() {
		c.logger.Debugf("active recordings detected (%d), skipping cloudconvert tasks", actives)
		return
	}
	maxActives := config.ReadOnly.CloudConvertAllowDuringRecordingMaxActiveRecordings()
	c.logger.Debugf("active recordings detected (%d), require <= %d to run cloudconvert during recording, skipping tasks", actives, maxActives)
}

func (c *cloudConvertManager) StartWorker(ctx context.Context, wg *sync.WaitGroup, db *db.Client) error {
	if c.client == nil {
		return ErrCloudConvertNotConfigured
	}
	bucket, err := db.Bucket(cloudConvertBucket)
	if err != nil {
		return err
	}
	c.bucket = bucket
	wg.Add(1)
	go c.checkTaskStatusPeriodically(ctx, wg)
	return nil
}

func (c *cloudConvertManager) Enqueue(inputPath, outputPath, format string, deleteSource bool) (*TaskQueue, error) {
	uuid, err := utils.NewUUIDv4()
	if err != nil {
		return nil, err
	}

	originalFormat := utils.GetPathFormat(inputPath)

	queue := &TaskQueue{
		Provider:      ProviderCloudConvert,
		TaskID:        uuid,
		InputPath:     inputPath,
		InputFileSize: fileSize(inputPath),
		OutputPath:    outputPath,
		InputFormat:   originalFormat,
		OutputFormat:  format,
		DeleteSource:  deleteSource,
	}

	data, err := c.serializer.Serialize(queue)
	if err != nil {
		return nil, err
	}

	err = c.bucket.Put([]byte(uuid), data)
	return queue, err
}

func (c *cloudConvertManager) submitTask(queue *TaskQueue) error {
	url, err := c.getOrCreatePresignedURL(queue.InputPath)
	if err != nil {
		return err
	}

	job, err := c.client.NewJobBuilder().
		AddTask(cloudconvert.NewImportURLTask(importTaskName, &cloudconvert.ImportURLRequest{
			URL:      url,
			Filename: filepath.Base(queue.InputPath),
		})).
		AddTask(cloudconvert.NewCommandTask(commandTaskName, &cloudconvert.CommandPayload{
			Input:         importTaskName,
			Engine:        "ffmpeg",
			Command:       "ffmpeg",
			EngineVersion: "8.0.1",
			Arguments: fmt.Sprintf(
				"-i \"/input/%s/%s\" -map 0:v? -map 0:a? -movflags +faststart -c copy \"/output/%s\"",
				importTaskName,
				filepath.Base(queue.InputPath),
				filepath.Base(queue.OutputPath),
			),
		})).
		AddTask(cloudconvert.NewExportURLTask(exportTaskName, &cloudconvert.ExportURLRequest{
			Input: commandTaskName,
		})).
		Submit()
	if err != nil {
		return err
	}

	convertTaskID := job.TaskID(commandTaskName)
	exportTaskID := job.TaskID(exportTaskName)
	oldID := queue.TaskID

	queue.TaskID = exportTaskID
	queue.ConvertTaskID = convertTaskID

	data, err := c.serializer.Serialize(queue)
	if err != nil {
		return err
	}

	err = c.bucket.Update(func(bucket *bbolt.Bucket) error {
		if err := bucket.Delete([]byte(oldID)); err != nil {
			return err
		}
		return bucket.Put([]byte(exportTaskID), data)
	})

	if err != nil {
		if cancelErr := c.client.CancelTask(utils.EmptyOrElse(convertTaskID, exportTaskID)); cancelErr != nil {
			return errors.Join(err, cancelErr)
		}
		return err
	}

	return nil
}

func (c *cloudConvertManager) Cancel(taskID string) error {
	var convertTaskID string
	if err := c.bucket.View(func(bucket *bbolt.Bucket) error {
		v := bucket.Get([]byte(taskID))
		if v == nil {
			return ErrTaskNotFound
		}
		var queue TaskQueue
		if err := c.serializer.Deserialize(v, &queue); err != nil {
			return fmt.Errorf("反序列化任务 %s 失败：%w", string(taskID), err)
		}
		convertTaskID = queue.ConvertTaskID
		return nil
	}); err != nil {
		return err
	}
	if convertTaskID != "" {
		if err := c.client.CancelTask(utils.EmptyOrElse(convertTaskID, taskID)); err != nil {
			return err
		}
	}
	return c.bucket.Delete([]byte(taskID))
}

func (c *cloudConvertManager) ListInProgress() ([]*TaskQueue, error) {
	var queues []*TaskQueue
	err := c.bucket.ForEach(func(k, v []byte) error {
		var queue TaskQueue
		if err := c.serializer.Deserialize(v, &queue); err != nil {
			return fmt.Errorf("反序列化任务 %s 失败：%w", string(k), err)
		}
		queues = append(queues, &queue)
		return nil
	})
	return queues, err
}

func (c *cloudConvertManager) InProgressSize() int {
	count, _ := c.bucket.Count()
	return count
}

func (c *cloudConvertManager) checkTaskStatusPeriodically(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(time.Duration(config.ReadOnly.CloudConvertCheckIntervalSecs()) * time.Second)
	defer ticker.Stop()

	var swg sync.WaitGroup
	defer swg.Wait()

	for {
		select {
		case <-ticker.C:
			c.logger.Debugf("checking task queue...")
			actives := c.getActives()
			if list, err := c.ListInProgress(); err != nil {
				c.logger.Errorf("列出进行中的任务失败：%v", err)
			} else {
				for _, queue := range list {
					if queue.ConvertTaskID == "" {
						if actives > 0 && !c.allowDuringRecording(actives) {
							c.logSkipDuringRecording(actives)
							continue
						}
						taskLog := c.logger.WithField("task_id", queue.TaskID)
						taskLog.Infof("正在提交 cloudconvert 任务 input=%s output=%s", queue.InputPath, queue.OutputPath)
						if err := c.submitTask(queue); err != nil {
							taskLog.Errorf("提交 cloudconvert 任务失败：%v", err)
						}
						continue
					}

					id := queue.TaskID
					if c.processing.Add(id) {
						c.logger.Debugf("task id=%v is being handled, skip status check", id)
						continue
					}

					c.logger.Debugf("checking task queue for id=%v", id)
					info, err := c.client.GetTask(id)
					if err != nil {
						if err == cloudconvert.ErrTaskNotFound {
							c.logger.Warnf("任务 id=%v 未找到，正在重新入队", id)
							swg.Go(func() {
								c.asyncOnFailed(queue, cloudconvert.TaskData{Message: utils.Ptr("任务未找到")})
							})
						} else {
							c.logger.Errorf("获取任务 id=%v 信息失败：%v", id, err)
							c.processing.Remove(id)
						}
						continue
					}

					c.logger.Infof("任务 id=%v 状态=%v", id, info.Data.Status)

					switch info.Data.Status {
					case cloudconvert.TaskStatusFinished:
						if actives > 0 && !c.allowDuringRecording(actives) {
							c.logSkipDuringRecording(actives)
							c.processing.Remove(id)
							continue
						}
						swg.Go(func() {
							c.asyncOnFinished(ctx, queue, info.Data)
						})
					case cloudconvert.TaskStatusError:
						swg.Go(func() {
							c.asyncOnFailed(queue, info.Data)
						})
					default:
						c.processing.Remove(id)
					}
				}
			}

		case <-ctx.Done():
			return
		}
	}
}

func (c *cloudConvertManager) asyncOnFinished(ctx context.Context, queue *TaskQueue, data cloudconvert.TaskData) {
	defer c.processing.Remove(queue.TaskID)
	if err := c.handleFinished(ctx, queue, &data); err != nil {
		c.logger.Errorf("处理任务 id=%v 状态=%v 失败：%v", queue.TaskID, data.Status, err)
	}
}

func (c *cloudConvertManager) asyncOnFailed(queue *TaskQueue, data cloudconvert.TaskData) {
	defer c.processing.Remove(queue.TaskID)
	if err := c.handleFailed(queue, &data); err != nil {
		c.logger.Errorf("处理任务 id=%v 状态=%v 失败：%v", queue.TaskID, data.Status, err)
	}
}

func (c *cloudConvertManager) handleFinished(ctx context.Context, queue *TaskQueue, data *cloudconvert.TaskData) error {
	// download file
	var download *cloudconvert.TaskResultFile
	if len(data.Result.Files) == 0 {
		return fmt.Errorf("任务 %s 没有结果文件", queue.TaskID)
	} else if len(data.Result.Files) == 1 {
		download = &data.Result.Files[0]
	} else {
		c.logger.Warnf("任务 %s 存在多个结果文件，将使用智能检测", queue.TaskID)
		// check output format and compare output path from TAskQueue
		for _, file := range data.Result.Files {
			format := utils.GetPathFormat(file.Filename)
			if filepath.Base(queue.OutputPath) == file.Filename {
				c.logger.Debugf("base(%s) == %s: matched filename", queue.OutputPath, file.Filename)
				download = &file
				break
			} else if format == queue.OutputFormat {
				c.logger.Debugf("format(%s) == %s: matched format", format, queue.OutputFormat)
				download = &file
				break
			}
		}
		if download == nil {
			c.logger.Debug("no matched filename or format, fallback to first file")
			download = &data.Result.Files[0]
		}
	}

	if err := c.downloadExportedFile(ctx, download.URL, queue.OutputPath); err != nil {
		c.logger.Errorf("下载任务 %s 的导出文件失败：%v", queue.TaskID, err)
		return err
	}

	if err := c.validateDownloadedOutputSize(queue); err != nil {
		c.logger.Warnf("任务 %s 下载文件校验失败：%v", queue.TaskID, err)
		msg := err.Error()
		return c.handleFailed(queue, &cloudconvert.TaskData{Message: &msg})
	}

	c.logger.Infof("已成功将任务 %s 的导出文件下载到 %s", queue.TaskID, queue.OutputPath)
	c.presignedUrlPool.Delete(queue.InputPath)

	err := utils.WithRetry(3, c.logger, "delete bucket", func() error {
		return c.bucket.Delete([]byte(queue.TaskID))
	})
	if err != nil {
		return err
	} else if !queue.DeleteSource || queue.InputPath == queue.OutputPath {
		return nil
	}

	return utils.WithRetry(3, c.logger, "delete source file", func() error {
		if !utils.IsFileExists(queue.InputPath) {
			c.logger.Debugf("source file %s does not exist, skipping delete", queue.InputPath)
			return nil
		}
		return os.Remove(queue.InputPath)
	})
}

func (c *cloudConvertManager) handleFailed(queue *TaskQueue, info *cloudconvert.TaskData) error {
	message := "unknown error"
	if info != nil && info.Message != nil {
		message = *info.Message
	}
	// print log and queue again
	c.logger.Errorf("任务 %s 失败，消息：%s", queue.TaskID, message)
	c.logger.Infof("正在将任务 %s 重新入队", queue.TaskID)

	deleteBucket := func() error {
		return utils.WithRetry(3, c.logger, "delete bucket", func() error {
			return c.bucket.Delete([]byte(queue.TaskID))
		})
	}

	if !utils.IsFileExists(queue.InputPath) {
		c.logger.Warnf("输入文件 %s 已不存在，取消任务 %s 的重试", queue.InputPath, queue.TaskID)
		return deleteBucket()
	}

	// enqueue again
	newInfo, err := c.Enqueue(queue.InputPath, queue.OutputPath, queue.OutputFormat, queue.DeleteSource)
	if err != nil {
		c.logger.Errorf("重新入队任务 %s 失败：%v", queue.TaskID, err)
		return err
	}

	c.logger.Infof("已将任务 %s 重新入队为新任务 %s", queue.TaskID, newInfo.TaskID)

	err = deleteBucket()

	if err != nil {
		// cancel re-enqueued task if we failed to delete old one
		c.logger.Warnf("删除旧任务 %s 失败，正在取消重新入队任务 %s", newInfo.TaskID, queue.TaskID)
		if cancelErr := c.Cancel(newInfo.TaskID); cancelErr != nil {
			c.logger.Errorf("取消重新入队任务 %s 失败：%v", newInfo.TaskID, cancelErr)
		}
		return err
	}

	return nil
}

func (c *cloudConvertManager) validateDownloadedOutputSize(queue *TaskQueue) error {
	if err := ValidateOutputFileSize(queue.InputPath, queue.OutputPath); err == nil {
		return nil
	} else {
		reason := err.Error()
		if removeErr := os.Remove(queue.OutputPath); removeErr != nil && !os.IsNotExist(removeErr) {
			c.logger.Warnf("移除无效下载输出 %s 失败：%v", queue.OutputPath, removeErr)
		}
		return errors.New(reason)
	}
}

func (c *cloudConvertManager) downloadExportedFile(ctx context.Context, url, outPath string) error {
	if err := c.concurrent.Acquire(ctx, 1); err != nil {
		return err
	}
	defer c.concurrent.Release(1)

	// Open stream from CloudConvert client
	rc, err := c.client.DownloadAsFileStream(url)
	if err != nil {
		return err
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 12*time.Hour)
	defer cancel()

	writer := pool.NewFileStreamWriter(timeoutCtx, c.downloadPool)
	return writer.WriteToFile(rc, outPath, config.ReadOnly.DownloadWriterBufferSize())
}

func (c *cloudConvertManager) getOrCreatePresignedURL(inputPath string) (url string, err error) {
	if presignedURL, ok := c.presignedUrlPool.Load(inputPath); ok {
		if _, err = c.pathSvc.ParsePresignedURL(presignedURL); err == nil {
			url, err = presignedURL, nil
			return
		}
		c.presignedUrlPool.Delete(inputPath)
		c.logger.Debugf("presigned URL for %s expired or invalid, generating a new one", inputPath)
	}
	url, err = c.pathSvc.GeneratePresignedURL(inputPath, signeddownload.DefaultExpireAfter)
	if err == nil {
		if !utils.IsValidAbsoluteHTTPURL(url) {
			c.logger.Errorf("PUBLIC_BASE_URL 为空或无效，CloudConvert 需要有效的绝对预签名 URL")
			return "", ErrInvalidPublicBaseURL
		}
	}
	if err == nil {
		c.presignedUrlPool.Store(inputPath, url)
	}
	return
}
