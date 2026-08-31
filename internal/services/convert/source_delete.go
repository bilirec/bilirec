package convert

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"

	"github.com/bilirec/bilirec/pkg/backoff"
	"github.com/bilirec/bilirec/pkg/ds"
	recfs "github.com/bilirec/bilirec/pkg/fs"
	"github.com/bilirec/bilirec/utils"
)

const (
	sourceDeleteMaxAttempts = 5
	sourceDeleteMarkSuffix  = "_轉換完成可刪除"
)

var removeSourceFile = func(path string) error {
	if !utils.IsFileExists(path) {
		return nil
	}
	return os.Remove(path)
}

var newSourceDeleteBackoff = func() *backoff.Expotential {
	return backoff.NewExpotential(1*time.Second, 2, 30*time.Second)
}

type sourceDeleter struct {
	ctx     context.Context
	wg      sync.WaitGroup
	pending ds.Set[string]
}

func newSourceDeleter(ctx context.Context) *sourceDeleter {
	return &sourceDeleter{
		ctx:     ctx,
		pending: ds.NewSyncedSet[string](),
	}
}

func (d *sourceDeleter) Wait() {
	d.wg.Wait()
}

func (d *sourceDeleter) Schedule(queue *TaskQueue, log logger.Logger) {
	if !queue.DeleteSource || queue.InputPath == queue.OutputPath {
		return
	}
	if d.pending.Add(queue.InputPath) {
		log.Debugf("源文件 %s 已在删除队列中，跳过", queue.InputPath)
		return
	}

	d.wg.Go(func() {
		defer d.pending.Remove(queue.InputPath)
		d.deleteSource(queue, log)
	})
}

func (d *sourceDeleter) deleteSource(queue *TaskQueue, log logger.Logger) {
	path := queue.InputPath
	bo := newSourceDeleteBackoff()

	var lastErr error
	for attempt := 1; attempt <= sourceDeleteMaxAttempts; attempt++ {
		if d.ctx.Err() != nil {
			log.Debugf("服务正在停止，中止删除 %s", path)
			return
		}

		err := removeSourceFile(path)
		if err == nil {
			log.Debugf("已删除源文件 %s", path)
			recfs.NotifyFileChanged(path)
			return
		}
		lastErr = err

		if attempt == sourceDeleteMaxAttempts {
			break
		}

		log.Warnf("删除源文件第 %d/%d 次尝试失败 %s：%v", attempt, sourceDeleteMaxAttempts, path, err)

		select {
		case <-d.ctx.Done():
			log.Debugf("服务正在停止，退避等待期间中止删除 %s", path)
			return
		case <-time.After(bo.Next()):
		}
	}

	d.handleDeleteFailure(queue, log, lastErr)
}

func (d *sourceDeleter) handleDeleteFailure(queue *TaskQueue, log logger.Logger, err error) {
	renamed, renameErr := markSourceForManualDelete(queue.InputPath)
	if renameErr != nil {
		log.Errorf("在 %d 次尝试后仍无法删除源文件 %s（最后错误：%v），重命名也失败：%v",
			sourceDeleteMaxAttempts, queue.InputPath, err, renameErr)
		return
	}
	recfs.NotifyFileChanged(queue.InputPath)
	recfs.NotifyFileChanged(renamed)
	log.Errorf("在 %d 次尝试后仍无法删除源文件 %s（最后错误：%v），已将其重命名为 %s，请手动删除",
		sourceDeleteMaxAttempts, queue.InputPath, err, renamed)
}

func markSourceForManualDelete(path string) (string, error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]

	target := filepath.Join(dir, stem+sourceDeleteMarkSuffix+ext)
	if !utils.IsFileExists(target) {
		if err := os.Rename(path, target); err != nil {
			return "", err
		}
		return target, nil
	}

	for i := 2; ; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s%s-%d%s", stem, sourceDeleteMarkSuffix, i, ext))
		if !utils.IsFileExists(candidate) {
			if err := os.Rename(path, candidate); err != nil {
				return "", err
			}
			return candidate, nil
		}
	}
}
