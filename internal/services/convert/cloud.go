package convert

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/eric2788/bilirec/internal/modules/config"
	"github.com/eric2788/bilirec/internal/services/path"
	"github.com/eric2788/bilirec/pkg/cloudconvert"
	"github.com/eric2788/bilirec/pkg/db"
	"github.com/eric2788/bilirec/pkg/ds"
	"github.com/eric2788/bilirec/pkg/pool"
	"github.com/eric2788/bilirec/pkg/signeddownload"
	"github.com/eric2788/bilirec/utils"
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

	processing   ds.AtomicSet[string]
	downloadPool *pool.BytesPool
	concurrent   *semaphore.Weighted

	presignedUrlPool *xsync.Map[string, string] // inputPath -> presignedURL

	pathSvc *path.Service
}

func newCloudConvertManager(client *cloudconvert.Client, pathSvc *path.Service) ConvertManager {
	return &cloudConvertManager{
		logger:           logger.WithField("manager", "cloudconvert"),
		client:           client,
		serializer:       pool.NewSerializer(),
		processing:       ds.NewAtomicSet[string](),
		downloadPool:     pool.NewBytesPool(config.ReadOnly.DownloadBufferSize()),
		concurrent:       semaphore.NewWeighted(int64(config.ReadOnly.CloudConvertMaxConcurrentDownloads())),
		presignedUrlPool: xsync.NewMap[string, string](),
		pathSvc:          pathSvc,
	}
}

func (c *cloudConvertManager) StartWorker(ctx context.Context, db *db.Client) error {
	if c.client == nil {
		return ErrCloudConvertNotConfigured
	}
	bucket, err := db.Bucket(cloudConvertBucket)
	if err != nil {
		return err
	}
	c.bucket = bucket
	go c.checkTaskStatusPeriodically(ctx)
	return nil
}

func (c *cloudConvertManager) Enqueue(inputPath, outputPath, format string, deleteSource bool) (*TaskQueue, error) {
	url, err := c.getOrCreatePresignedURL(inputPath)
	if err != nil {
		return nil, err
	}

	originalFormat := filepath.Ext(inputPath)[1:]

	job, err := c.client.NewJobBuilder().
		AddTask(cloudconvert.NewImportURLTask(importTaskName, &cloudconvert.ImportURLRequest{
			URL:      url,
			Filename: filepath.Base(inputPath),
		})).
		AddTask(cloudconvert.NewCommandTask(commandTaskName, &cloudconvert.CommandPayload{
			Input:         importTaskName,
			Engine:        "ffmpeg",
			Command:       "ffmpeg",
			EngineVersion: "8.0.1",
			Arguments: fmt.Sprintf(
				"-i \"/input/%s/%s\" -map 0 -map_metadata 0 -movflags +faststart -c copy \"/output/%s\"",
				importTaskName,
				filepath.Base(inputPath),
				filepath.Base(outputPath),
			),
		})).
		AddTask(cloudconvert.NewExportURLTask(exportTaskName, &cloudconvert.ExportURLRequest{
			Input: commandTaskName,
		})).
		Submit()
	if err != nil {
		return nil, err
	}

	convertTaskID := job.TaskID(commandTaskName)
	exportTaskID := job.TaskID(exportTaskName)

	queue := &TaskQueue{
		Provider:      ProviderCloudConvert,
		TaskID:        exportTaskID,
		ConvertTaskID: convertTaskID,
		InputPath:     inputPath,
		OutputPath:    outputPath,
		InputFormat:   originalFormat,
		OutputFormat:  format,
		DeleteSource:  deleteSource,
	}

	data, err := c.serializer.Serialize(queue)
	if err != nil {
		return nil, err
	}

	err = c.bucket.Put([]byte(queue.TaskID), data)
	return queue, err
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
			return fmt.Errorf("deserialize task %s: %w", string(taskID), err)
		}
		convertTaskID = queue.ConvertTaskID
		return nil
	}); err != nil {
		return err
	}
	if err := c.client.CancelTask(utils.EmptyOrElse(convertTaskID, taskID)); err != nil {
		return err
	}
	return c.bucket.Delete([]byte(taskID))
}

func (c *cloudConvertManager) ListInProgress() ([]*TaskQueue, error) {
	var queues []*TaskQueue
	err := c.bucket.ForEach(func(k, v []byte) error {
		var queue TaskQueue
		if err := c.serializer.Deserialize(v, &queue); err != nil {
			return fmt.Errorf("deserialize task %s: %w", string(k), err)
		}
		queues = append(queues, &queue)
		return nil
	})
	return queues, err
}

func (c *cloudConvertManager) checkTaskStatusPeriodically(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(config.ReadOnly.CloudConvertCheckIntervalSecs()) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.logger.Debugf("checking task queue...")
			if list, err := c.ListInProgress(); err != nil {
				c.logger.Errorf("failed to list in-progress tasks: %v", err)
			} else {
				for _, queue := range list {
					id := queue.TaskID
					if c.processing.LoadAndStore(id) {
						c.logger.Debugf("task id=%v is being handled, skip status check", id)
						continue
					}

					c.logger.Debugf("checking task queue for id=%v", id)
					info, err := c.client.GetTask(id)
					if err != nil {
						if err == cloudconvert.ErrTaskNotFound {
							c.logger.Warnf("task id=%v not found, re-enqueueing", id)
							go c.asyncOnFailed(queue, cloudconvert.TaskData{Message: utils.Ptr("task not found")})
						} else {
							c.logger.Errorf("failed to get task info for id=%v: %v", id, err)
							c.processing.LoadAndDelete(id)
						}
						continue
					}

					c.logger.Infof("task id=%v status=%v", id, info.Data.Status)

					switch info.Data.Status {
					case cloudconvert.TaskStatusFinished:
						go c.asyncOnFinished(ctx, queue, info.Data)
					case cloudconvert.TaskStatusError:
						go c.asyncOnFailed(queue, info.Data)
					default:
						c.processing.LoadAndDelete(id)
					}
				}
			}

		case <-ctx.Done():
			return
		}
	}
}

func (c *cloudConvertManager) asyncOnFinished(ctx context.Context, queue *TaskQueue, data cloudconvert.TaskData) {
	defer c.processing.LoadAndDelete(queue.TaskID)
	if err := c.handleFinished(ctx, queue, &data); err != nil {
		c.logger.Errorf("handling task id=%v status=%v failed: %v", queue.TaskID, data.Status, err)
	}
}

func (c *cloudConvertManager) asyncOnFailed(queue *TaskQueue, data cloudconvert.TaskData) {
	defer c.processing.LoadAndDelete(queue.TaskID)
	if err := c.handleFailed(queue, &data); err != nil {
		c.logger.Errorf("handling task id=%v status=%v failed: %v", queue.TaskID, data.Status, err)
	}
}

func (c *cloudConvertManager) handleFinished(ctx context.Context, queue *TaskQueue, data *cloudconvert.TaskData) error {
	// download file
	var download *cloudconvert.TaskResultFile
	if len(data.Result.Files) == 0 {
		return fmt.Errorf("no result files for task %s", queue.TaskID)
	} else if len(data.Result.Files) == 1 {
		download = &data.Result.Files[0]
	} else {
		c.logger.Warnf("multiple result files for task %s, will use smart detect", queue.TaskID)
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
		c.logger.Errorf("failed to download exported file for task %s: %v", queue.TaskID, err)
		return err
	}

	if err := c.validateDownloadedOutputSize(queue); err != nil {
		c.logger.Warnf("downloaded file validation failed for task %s: %v", queue.TaskID, err)
		msg := err.Error()
		return c.handleFailed(queue, &cloudconvert.TaskData{Message: &msg})
	}

	c.logger.Infof("successfully downloaded exported file for task %s to %s", queue.TaskID, queue.OutputPath)
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
	c.logger.Errorf("task %s failed with message: %s", queue.TaskID, message)
	c.logger.Infof("re-enqueueing task %s", queue.TaskID)

	deleteBucket := func() error {
		return utils.WithRetry(3, c.logger, "delete bucket", func() error {
			return c.bucket.Delete([]byte(queue.TaskID))
		})
	}

	if !utils.IsFileExists(queue.InputPath) {
		c.logger.Warnf("input file %s no longer exists, cancelling retry for task %s", queue.InputPath, queue.TaskID)
		return deleteBucket()
	}

	// enqueue again
	newInfo, err := c.Enqueue(queue.InputPath, queue.OutputPath, queue.OutputFormat, queue.DeleteSource)
	if err != nil {
		c.logger.Errorf("failed to re-enqueue task %s: %v", queue.TaskID, err)
		return err
	}

	c.logger.Infof("re-enqueued task %s as new task %s", queue.TaskID, newInfo.TaskID)

	err = deleteBucket()

	if err != nil {
		// cancel re-enqueued task if we failed to delete old one
		c.logger.Warnf("cancelling re-enqueued task %s due to failure in deleting old task %s", newInfo.TaskID, queue.TaskID)
		if cancelErr := c.Cancel(newInfo.TaskID); cancelErr != nil {
			c.logger.Errorf("failed to cancel re-enqueued task %s: %v", newInfo.TaskID, cancelErr)
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
			c.logger.Warnf("failed to remove invalid downloaded output %s: %v", queue.OutputPath, removeErr)
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
		c.presignedUrlPool.Store(inputPath, url)
	}
	return
}
