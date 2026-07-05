package recorder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	rs "github.com/bilirec/bilirec/internal/record_strategies"
	"github.com/bilirec/bilirec/internal/services/convert"
	"github.com/bilirec/bilirec/internal/services/notify"
	"github.com/bilirec/bilirec/internal/services/stream"
	"github.com/bilirec/bilirec/pkg/ds"
	"github.com/bilirec/bilirec/pkg/pipeline"
	"github.com/bilirec/bilirec/pkg/tx"
	"github.com/bilirec/bilirec/utils"
	"github.com/puzpuzpuz/xsync/v4"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

var logger = logrus.WithField("service", "recorder")

type RecordStatus string

const Recording RecordStatus = "recording"
const Recovering RecordStatus = "recovering"
const Idle RecordStatus = "idle"

// var idlePtr *RecordStatus = utils.Ptr(Idle)
var recordingPtr *RecordStatus = utils.Ptr(Recording)
var recoveringPtr *RecordStatus = utils.Ptr(Recovering)

var ErrMaxConcurrentRecordingsReached = errors.New("已达到最大并发录制数")
var ErrRecordingStarted = errors.New("录制已开始")
var ErrRecordRecovering = errors.New("录制正在恢复流")
var ErrRecordingPending = errors.New("录制正在启动中")
var ErrStreamNotLive = errors.New("该房间当前未在直播")
var ErrEmptyStreamURLs = errors.New("没有可用的流 URL")
var ErrStreamURLsUnreachable = errors.New("所有流 URL 均不可达")
var ErrRoomBanned = errors.New("该房间已被封禁")
var ErrRoomEncrypted = errors.New("该房间已加密")
var ErrInsufficientDiskSpace = errors.New("磁盘空间不足")

type Service struct {
	st           *stream.Service
	cv           *convert.Service
	nt           *notify.Service
	bilic        *bilibili.Client
	recording    *xsync.Map[int, *Info]
	writingFiles ds.Set[string]
	pipes        *xsync.Map[int, *pipeline.Pipe[[]byte]]

	cfg   *config.Config
	ctx   context.Context
	wg    sync.WaitGroup
	reser tx.Coordinator[int]
}

func NewService(
	lc fx.Lifecycle,
	st *stream.Service,
	cv *convert.Service,
	nt *notify.Service,
	bilic *bilibili.Client,
	cfg *config.Config,
) *Service {

	ctx, cancel := context.WithCancel(context.Background())

	r := &Service{
		st:           st,
		cv:           cv,
		nt:           nt,
		bilic:        bilic,
		recording:    xsync.NewMap[int, *Info](),
		writingFiles: ds.NewSyncedSet[string](),
		pipes:        xsync.NewMap[int, *pipeline.Pipe[[]byte]](),
		cfg:          cfg,
		ctx:          ctx,
	}

	r.reser = tx.NewPending(
		func(roomId int, pendingStart ds.Set[int]) error {
			if status := r.GetStatus(roomId); status == Recording {
				return ErrRecordingStarted
			}

			if (r.recording.Size() + pendingStart.Size()) > r.cfg.MaxConcurrentRecordings {
				if existing, ok := r.recording.Load(roomId); !ok { // if not recovering existing recording
					return ErrMaxConcurrentRecordingsReached
				} else if status := existing.status.Load(); status != recoveringPtr { // not recovering
					return ErrMaxConcurrentRecordingsReached
				}
			}

			return nil
		},
	)

	cv.SetActiveRecordingsGetter(r.ListRecordingSize)

	lc.Append(fx.StopHook(func() {
		cancel()
		r.wg.Wait()
	}))
	return r
}

func (r *Service) Start(roomId int, options ...RecordStartOption) error {
	switch r.GetStatus(roomId) {
	case Recording:
		return ErrRecordingStarted
	case Recovering:
		return ErrRecordRecovering
	}

	startOptions := newRecordStartOptions()
	for _, option := range options {
		if option != nil {
			option(&startOptions)
		}
	}

	ctx, cancel := context.WithCancel(r.ctx)
	adopted := false
	defer func() {
		if !adopted {
			cancel()
		}
	}()

	err := r.internalStart(internalStartParams{
		roomId: roomId,
		opts:   startOptions,
		ctx:    ctx,
		cancel: cancel,
		mode:   startModeUser,
	})
	if err == nil {
		adopted = true
	}
	return err
}

func (r *Service) Stop(roomId int) bool {

	info, hasRecording := r.recording.LoadAndDelete(roomId)
	pipe, hasPipe := r.pipes.LoadAndDelete(roomId)

	if hasRecording {
		info.cancel()
	} else {
		logger.Warnf("未找到房间 %d 的录制任务", roomId)
	}

	if hasPipe && !hasRecording {
		logger.Warnf("发现房间 %d 的孤立管道，正在关闭...", roomId)
		pipe.Close()
	}

	return hasRecording
}

func (r *Service) prepare(roomId int, ch <-chan []byte, strategy rs.StreamRecordStrategy, ctx context.Context, info *Info, scheduleDurationCheck bool) error {

	r.wg.Go(func() {
		defer r.recover(roomId)
		err := r.rotate(roomId, ch, strategy, info, ctx)
		if err != nil {
			logger.Errorf("轮转录制失败：%v", err)
		}
	})

	if scheduleDurationCheck {
		go r.checkRecordingDurationPeriodically(roomId, ctx, info.maxDuration)
	}
	return nil
}

func (r *Service) rotate(roomId int, ch <-chan []byte, strategy rs.StreamRecordStrategy, info *Info, ctx context.Context) error {
	l := logger.WithField("room", roomId)
	defer strategy.Close()

	segment := 0
	state := &rs.RotationState{Data: map[string][]byte{}}

	for {
		outputPath, err := r.rotateFilePath(info, segment, strategy.FileExtension())
		if err != nil {
			return fmt.Errorf("无法准备文件路径：%v", err)
		}
		info.SetOutputPath(outputPath)

		pipe, err := strategy.BuildPipeline(ctx, outputPath, state)
		if err != nil {
			return fmt.Errorf("无法构建管道：%v", err)
		}

		startCtx, startCancel := context.WithTimeout(ctx, 10*time.Second)
		if err := pipe.Open(startCtx); err != nil {
			startCancel()
			return fmt.Errorf("无法打开管道：%v", err)
		}
		startCancel()

		r.writingFiles.Add(filepath.Base(info.OutputPath()))
		r.pipes.Store(roomId, pipe)

		err = r.rev(roomId, ch, info, ctx, pipe)
		if err != nil {
			handle := strategy.HandleErr(err)
			switch handle.Action {
			case rs.ErrActionRotate:
				l.Infof("收到策略要求轮转文件的信号，开始分割录制文件: %v", err)
				if handle.State == nil {
					state = &rs.RotationState{Data: map[string][]byte{}}
				} else {
					state = handle.State
				}
				segment++
				continue
			case rs.ErrActionAbort:
				l.Infof("收到录制停止信号：%v", err)
				if handle.AbortDelay > 0 {
					timer := time.NewTimer(handle.AbortDelay)
					select {
					case <-timer.C:
					case <-ctx.Done():
						timer.Stop()
					}
				}
			default:
				l.Errorf("写入文件失败：%v", err)
			}
		}
		break
	}
	return nil
}

func (r *Service) rev(roomId int, ch <-chan []byte, info *Info, ctx context.Context, pipe *pipeline.Pipe[[]byte]) error {
	log := logger.WithField("room", roomId)
	defer func() {
		pipe.Close()
		outputPath := info.OutputPath()
		go r.finalize(roomId, outputPath)
	}()
	for data := range ch {
		info.bytesRead.Add(uint64(len(data)))
		result, err := pipe.Process(ctx, data)
		if info.chunkPool != nil && cap(data) > 0 {
			info.chunkPool.Put(data[:cap(data)])
		}
		if err != nil {
			// "abandoned" bytes = pipeline output size at the moment of error, not input size.
			// Observed values:
			//   0B    — split at chunk boundary, no carried bytes (most common, harmless)
			//   ~4.6KB — carried bytes from a rotation split; replayed into next segment, not lost
			// At 6 Mbps, 5000B ≈ 6 ms ≈ <0.25 frame at 60 fps — imperceptible in recordings.
			if len(result) > 0 {
				log.Warnf("已丢弃流数据分片：%dB", len(result))
			}
			return err
		}
	}
	return nil
}

func (r *Service) checkRecordingDurationPeriodically(roomId int, ctx context.Context, maxDuration time.Duration) {
	log := logger.WithField("room", roomId)

	// 0 means unlimited — skip the time-limit loop entirely
	if maxDuration == 0 {
		log.Info("录制时长：无限制")
		return
	}

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			info, ok := r.recording.Load(roomId)
			if !ok {
				return
			}
			elapsed := time.Since(info.startTime)
			if elapsed >= maxDuration {
				log.Infof("已达到最大录制时长（%v），正在停止", elapsed.Round(time.Minute))
				r.stopAndPublish(roomId, info)
				return
			}

			if int(elapsed.Minutes())%30 == 0 {
				remaining := maxDuration - elapsed
				log.Infof("录制中：已用时 %v，剩余 %v，%d MB", elapsed.Round(time.Minute), remaining.Round(time.Minute), info.bytesRead.Load()/1024/1024)
			}

		case <-ctx.Done():
			return
		}
	}
}

// Note: Each recovery attempt creates a NEW file with a new timestamp.
// This is intentional - we want separate files for each recording segment
// rather than appending to the same file. Multiple files per session is expected.
func (r *Service) recover(roomId int) {
	l := logger.WithField("room", roomId)
	info, ok := r.recording.Load(roomId)
	if !ok {
		l.Debugf("未找到录制任务，跳过恢复")
		return
	} else if status := info.status.Load(); status == recoveringPtr {
		l.Infof("当前正在恢复流，跳过本次恢复")
		return
	}
	l.Infof("正在尝试恢复流录制...")

	info.status.Store(recoveringPtr)
	attempt := 1
	retryStart := time.Now()
	for {
		if err := info.ctx.Err(); err != nil {
			l.Infof("录制任务已停止，终止恢复")
			return
		}

		err := r.internalStart(internalStartParams{
			roomId:  roomId,
			opts:    info.startOptions,
			ctx:     info.ctx,
			mode:    startModeRecovery,
			session: info,
		})
		if err == nil {
			l.Info("直播流恢复成功")
			info.backoff.Reset()
			return
		}

		switch err {
		case ErrMaxConcurrentRecordingsReached:
			l.Infof("因以下原因停止恢复：%v", err)
			r.stopAndPublish(roomId, info)
			return
		case ErrRoomEncrypted, ErrRoomBanned:
			l.Infof("直播间已封禁或为付费直播，不再恢复")
			r.stopAndPublish(roomId, info)
			return
		default:

			// Should check if recording was manually stopped
			if _, ok := r.recording.Load(roomId); !ok || errors.Is(err, context.Canceled) {
				l.Infof("重试期间录制任务已移除，不再恢复")
				return
			}

			nextSleep := info.backoff.Next()

			// if the error is stream not live, we should retry until max retry minutes reached, instead of max attempts, since the stream may be live again after some time
			if err == ErrStreamNotLive {
				// use r.cfg.MaxRetryMinutes to limit the total retry duration, instead of max attempts, since the stream may be live again after some time
				if time.Since(retryStart) >= time.Duration(r.cfg.MaxRetryMinutes)*time.Minute {
					l.Infof("直播已下线且已达到最长重试时间 (%d 分钟)，不再恢复", r.cfg.MaxRetryMinutes)
					r.stopAndPublish(roomId, info)
					return
				}
			} else if attempt >= r.cfg.MaxRecoveryAttempts {
				l.Infof("已达到最大恢复次数（%d），不再恢复", r.cfg.MaxRecoveryAttempts)
				r.stopAndPublish(roomId, info)
				return
			} else {
				l.Warnf("第 %d 次恢复失败：%v", attempt, err)
				l.Infof("将在 %d 秒后重试恢复流录制...", int(nextSleep.Seconds()))
			}

			timer := time.NewTimer(nextSleep)
			select {
			case <-timer.C:
				attempt++
				continue
			case <-info.ctx.Done():
				l.Infof("录制任务已停止，终止恢复流程")
				timer.Stop()
				return
			case <-r.ctx.Done():
				l.Infof("服务正在停止，终止恢复流程")
				timer.Stop()
				return
			}

		}
	}
}

func (r *Service) finalize(roomId int, outputPath string) {
	if outputPath == "" {
		logger.Warnf("跳过房间 %d 的收尾：输出路径为空", roomId)
		return
	}

	defer r.writingFiles.Remove(filepath.Base(outputPath))

	fileInfo, err := os.Stat(outputPath)
	if err != nil && config.ReadOnly.SkipSmallFlush() && os.IsNotExist(err) {
		logger.Debugf("文件因为过小被而没有写入，跳过收尾：%s", outputPath)
		return
	} else if err != nil {
		logger.Errorf("获取房间 %d 录制文件状态失败：%v", roomId, err)
		return
	} else if fileInfo.Size() < 1024 { // less than 1KB
		logger.Warnf("房间 %d 的录制文件过小（%d 字节），跳过收尾并删除文件", roomId, fileInfo.Size())
		if err := os.Remove(outputPath); err != nil {
			logger.Errorf("删除空文件 %s 失败：%v", outputPath, err)
		}
		return
	}

	if !r.cfg.ConvertToMp4 {
		logger.Debug("不需要转换为 mp4，跳过收尾")
		return
	}

	// 跳过已经转换为 mp4 的文件
	if filepath.Ext(outputPath) == ".mp4" {
		logger.Debugf("已经转换为 mp4，跳过收尾: %s", outputPath)
		return
	}

	if r.ctx.Err() != nil {
		logger.Infof("服务正在停止，跳过房间 %d 的入队转码", roomId)
		return
	}

	// process finalization via convert service
	if queue, err := r.cv.Enqueue(outputPath, "mp4", r.cfg.DeleteSourceAfterConvert); err != nil {
		logger.Errorf("为房间 %d 入队转码失败：%v", roomId, err)
		logger.Warnf("你可能需要为房间 %d 手动转码 mp4", roomId)
	} else {
		logger.Infof("已为房间 %d 入队转码任务：%s", roomId, queue.TaskID)
		logger.Infof("输出路径将是：%s", queue.OutputPath)
	}
}

func (r *Service) stopAndPublish(roomId int, info *Info) {
	r.Stop(roomId)
	r.nt.PublishLiveState(roomId, info.room.Uname, info.room.Title, notify.LiveStateRecordStopped)
}

// the time should be the time you start the record, not live start
func (r *Service) rotateFilePath(info *Info, segment int, ext string) (string, error) {
	dirPath := fmt.Sprintf("%s/%s-%d", r.cfg.OutputDir, info.room.Uname, info.room.RoomID)
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return "", err
	}
	safeTitle := utils.TruncateString(utils.SanitizeFilename(info.room.Title), 20)
	if segment == 0 {
		return fmt.Sprintf("%s/%s-%s%s", dirPath, safeTitle, info.startTime.Format("20060102_150405"), ext), nil
	} else {
		return fmt.Sprintf("%s/%s-%s-%d%s", dirPath, safeTitle, info.startTime.Format("20060102_150405"), segment, ext), nil
	}
}
