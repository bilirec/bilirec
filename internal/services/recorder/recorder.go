package recorder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/eric2788/bilirec/internal/modules/bilibili"
	"github.com/eric2788/bilirec/internal/modules/config"
	rs "github.com/eric2788/bilirec/internal/record_strategies"
	"github.com/eric2788/bilirec/internal/services/convert"
	"github.com/eric2788/bilirec/internal/services/stream"
	"github.com/eric2788/bilirec/pkg/ds"
	"github.com/eric2788/bilirec/pkg/pipeline"
	"github.com/eric2788/bilirec/utils"
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
var ErrStreamNotLive = errors.New("该房间当前未在直播")
var ErrEmptyStreamURLs = errors.New("没有可用的流 URL")
var ErrStreamURLsUnreachable = errors.New("所有流 URL 均不可达")
var ErrRoomBanned = errors.New("该房间已被封禁")
var ErrRoomEncrypted = errors.New("该房间已加密")
var ErrInsufficientDiskSpace = errors.New("磁盘空间不足")
var ErrInvalidStreamProfile = errors.New("无效的流配置")

type Service struct {
	st           *stream.Service
	cv           *convert.Service
	bilic        *bilibili.Client
	recording    *xsync.Map[int, *Info]
	writingFiles ds.Set[string]
	pipes        *xsync.Map[int, *pipeline.Pipe[[]byte]]

	cfg *config.Config
	ctx context.Context
	wg  sync.WaitGroup
}

func NewService(
	lc fx.Lifecycle,
	st *stream.Service,
	cv *convert.Service,
	bilic *bilibili.Client,
	cfg *config.Config,
) *Service {

	ctx, cancel := context.WithCancel(context.Background())

	s := &Service{
		st:           st,
		cv:           cv,
		bilic:        bilic,
		recording:    xsync.NewMap[int, *Info](),
		writingFiles: ds.NewSyncedSet[string](),
		pipes:        xsync.NewMap[int, *pipeline.Pipe[[]byte]](),
		cfg:          cfg,
		ctx:          ctx,
	}

	cv.SetActiveRecordingsGetter(s.recording.Size)

	go s.backgroundMaintenance(ctx)

	lc.Append(fx.StopHook(func() {
		cancel()
		s.wg.Wait()
	}))
	return s
}

func (r *Service) Start(roomId int, options ...RecordStartOption) error {
	startOptions := newRecordStartOptions()
	for _, option := range options {
		if option != nil {
			option(&startOptions)
		}
	}

	if startOptions.hasStreamProfile {
		switch startOptions.streamProfile {
		case bilibili.ProfileHTTPFLV, bilibili.ProfileHLSTS, bilibili.ProfileHLSFMP4:
		default:
			return ErrInvalidStreamProfile
		}
	}

	l := logger.WithField("room", roomId)

	if status := r.GetStatus(roomId); status == Recording {
		return ErrRecordingStarted
	}

	if r.recording.Size() >= r.cfg.MaxConcurrentRecordings {
		if existing, ok := r.recording.Load(roomId); !ok { // if not recovering existing recording
			return ErrMaxConcurrentRecordingsReached
		} else if status := existing.status.Load(); status != recoveringPtr { // not recovering
			return ErrMaxConcurrentRecordingsReached
		}
	}

	// Check disk space - require at least configured minimum free space
	diskSpace, err := utils.GetDiskSpace(r.cfg.OutputDir)
	if err != nil {
		l.Warnf("cannot check disk space: %v", err)
	} else if diskSpace.Free < uint64(r.cfg.MinDiskSpaceBytes) {
		return ErrInsufficientDiskSpace
	}

	startTimeRoomInfo := time.Now()
	roomInfo, err := r.bilic.GetLiveRoomInfo(roomId)
	durationRoomInfo := time.Since(startTimeRoomInfo)
	l.Debugf("duration: function=GetLiveRoomInfo spent=%v", durationRoomInfo)
	if err != nil {
		return err
	} else if roomInfo.IsEncrypted {
		return ErrRoomEncrypted
	} else if roomInfo.LockStatus != 0 {
		return ErrRoomBanned
	} else if roomInfo.LiveStatus != 1 {
		return ErrStreamNotLive
	}

	var streams []bilibili.StreamURLInfo
	startTimeStreamURLs := time.Now()
	if startOptions.hasStreamProfile {
		streams, err = r.bilic.GetStreamURLsV2(roomId, bilibili.WithProfiles(startOptions.streamProfile))
	} else {
		streams, err = r.bilic.GetStreamURLsV2(roomId)
	}
	durationStreamURLs := time.Since(startTimeStreamURLs)
	l.Debugf("duration: function=GetStreamURLsV2 spent=%v", durationStreamURLs)

	if err != nil {
		return err
	} else if len(streams) == 0 {
		return ErrEmptyStreamURLs
	}

	// Prefer higher quality stream candidates first.
	sort.SliceStable(streams, func(i, j int) bool {
		return streams[i].Qn > streams[j].Qn
	})

	now := time.Now()
	ctx, cancel := context.WithCancel(r.ctx)

	// retry mechanism
	for idx, streamInfo := range streams {
		urlPreview := utils.TruncateString(streamInfo.URL, 160)

		l.Debugf("trying stream url [%d/%d]: protocol=%s, format=%s, codec=%s, qn=%d, url=%s",
			idx+1,
			len(streams),
			streamInfo.Protocol,
			streamInfo.Format,
			streamInfo.Codec,
			streamInfo.Qn,
			urlPreview,
		)

		var ch <-chan []byte
		var strategy rs.StreamRecordStrategy

		switch streamInfo.Format {
		case "ts", "fmp4":
			l.Debugf("stream response [%d/%d]: protocol=%s, format=%s, codec=%s, qn=%d, req=%s",
				idx+1,
				len(streams),
				streamInfo.Protocol,
				streamInfo.Format,
				streamInfo.Codec,
				streamInfo.Qn,
				urlPreview,
			)

			initialURL := streamInfo.URL

			profile := utils.Ternary(
				streamInfo.Format == "ts",
				bilibili.ProfileHLSTS,
				bilibili.ProfileHLSFMP4,
			)

			fetchM3u8URL := func() (string, error) {
				if initialURL != "" {
					url := initialURL
					initialURL = ""
					return url, nil
				}

				latestStreams, fetchErr := r.bilic.GetStreamURLsV2(roomId, bilibili.WithProfiles(profile))
				if fetchErr != nil {
					return "", fetchErr
				} else if len(latestStreams) == 0 {
					return "", nil
				}

				tryResolve := func(candidate bilibili.StreamURLInfo) (string, bool) {
					fetchCtx := ctx
					if _, ok := fetchCtx.Deadline(); !ok {
						var cancel context.CancelFunc
						fetchCtx, cancel = context.WithTimeout(fetchCtx, 3*time.Second)
						defer cancel()
					}
					m3u8Resp, fetchErr := r.bilic.GetLiveHlsPlaylistClient().R().SetContext(fetchCtx).Get(candidate.URL)
					if fetchErr != nil {
						return "", false
					}
					if body := m3u8Resp.RawBody(); body != nil {
						defer body.Close()
					}
					if m3u8Resp.StatusCode() != 200 {
						return "", false
					}

					if m3u8Resp.RawResponse != nil && m3u8Resp.RawResponse.Request != nil && m3u8Resp.RawResponse.Request.URL != nil {
						return m3u8Resp.RawResponse.Request.URL.String(), true
					}
					return candidate.URL, true
				}

				for _, candidate := range latestStreams {
					if candidate.Format != streamInfo.Format || candidate.Protocol != streamInfo.Protocol || candidate.Codec != streamInfo.Codec {
						continue
					}
					if refreshedURL, ok := tryResolve(candidate); ok {
						return refreshedURL, nil
					}
				}

				for _, candidate := range latestStreams {
					if candidate.Format != streamInfo.Format {
						continue
					}
					if refreshedURL, ok := tryResolve(candidate); ok {
						return refreshedURL, nil
					}
				}

				return "", nil
			}

			hlsCh, hlsErr := r.st.ReadHlsStream(fetchM3u8URL, r.bilic.GetLiveHlsPlaylistClient(), r.bilic.GetLiveHlsSegmentClient(), ctx)
			if hlsErr != nil {
				l.Errorf("cannot start HLS stream: %v, will try next url", hlsErr)
				continue
			}
			ch = hlsCh
			strategy = utils.TernaryFunc(
				streamInfo.Format == "ts",
				func() rs.StreamRecordStrategy { return rs.NewHlsTsStrategy() },
				func() rs.StreamRecordStrategy { return rs.NewHlsFmp4Strategy() },
			)
		default: // "flv" and any unknown format
			startTimeFlv := time.Now()
			resp, err := r.bilic.FetchLiveStreamUrlWithCtx(streamInfo.URL, ctx)
			durationFlv := time.Since(startTimeFlv)
			l.Debugf("duration: function=FetchLiveStreamUrlWithCtx spent=%v", durationFlv)

			if err != nil {
				l.Errorf("cannot fetch url: %v, will try next url (protocol=%s, format=%s, codec=%s, qn=%d, url=%s)",
					err,
					streamInfo.Protocol,
					streamInfo.Format,
					streamInfo.Codec,
					streamInfo.Qn,
					urlPreview,
				)
				continue
			} else if resp.StatusCode() != 200 {
				l.Errorf("non-200 response: %d, will try next url (protocol=%s, format=%s, codec=%s, qn=%d, url=%s)",
					resp.StatusCode(),
					streamInfo.Protocol,
					streamInfo.Format,
					streamInfo.Codec,
					streamInfo.Qn,
					urlPreview,
				)
				continue
			}

			finalURL := ""
			if resp.RawResponse != nil && resp.RawResponse.Request != nil && resp.RawResponse.Request.URL != nil {
				finalURL = resp.RawResponse.Request.URL.String()
			}

			l.Debugf("stream response [%d/%d]: status=%d, content-type=%s, protocol=%s, format=%s, codec=%s, qn=%d, req=%s, final=%s",
				idx+1,
				len(streams),
				resp.StatusCode(),
				resp.Header().Get("Content-Type"),
				streamInfo.Protocol,
				streamInfo.Format,
				streamInfo.Codec,
				streamInfo.Qn,
				urlPreview,
				utils.TruncateString(finalURL, 160),
			)

			flvCh, flvErr := r.st.ReadFlvStream(resp, ctx)
			if flvErr != nil {
				l.Errorf("cannot capture url stream: %v, will try next url", flvErr)
				continue
			}
			ch = flvCh
			strategy = rs.NewFlvStrategy()
		}

		// Resolve runtime max duration for recorder internals.
		// Internal semantics: 0 duration means unlimited.
		// API/config sentinel mapping (e.g. 0 default, -1 unlimited) is handled by callers.
		var maxDuration time.Duration
		if startOptions.hasDuration {
			maxDuration = startOptions.duration
		} else {
			maxDuration = time.Duration(r.cfg.MaxRecordingHours) * time.Hour
		}

		// initialize Recorder info
		info := &Info{
			cancel:      cancel,
			startTime:   now,
			room:        roomInfo,
			maxDuration: maxDuration,
		}
		info.SetOutputPath("") // initialize output path to empty string to avoid potential nil pointer dereference in finalize()
		return r.prepare(roomId, ch, strategy, ctx, info)
	}
	cancel()
	l.Warn("no more url left")
	return ErrStreamURLsUnreachable
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

func (r *Service) prepare(roomId int, ch <-chan []byte, strategy rs.StreamRecordStrategy, ctx context.Context, info *Info) error {

	info.status.Store(recordingPtr)
	r.recording.Store(roomId, info)

	r.wg.Go(func() {
		defer r.recover(roomId)
		defer info.cancel()
		err := r.rotate(roomId, ch, strategy, info, ctx)
		if err != nil {
			logger.Errorf("轮转录制失败：%v", err)
		}
	})

	go r.checkRecordingDurationPeriodically(roomId, ctx, info.maxDuration)
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
				l.Info("rotating file due to strategy error handling")
				if handle.State == nil {
					state = &rs.RotationState{Data: map[string][]byte{}}
				} else {
					state = handle.State
				}
				segment++
				continue
			case rs.ErrActionAbort:
				l.Warnf("strategy requested abort due to stream error: %v", err)
				if handle.AbortDelay > 0 {
					timer := time.NewTimer(handle.AbortDelay)
					select {
					case <-timer.C:
					case <-ctx.Done():
						timer.Stop()
					}
				}
			default:
				l.Errorf("error writing data to file: %v", err)
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
		r.st.Flush(data)
		if err != nil {
			// "abandoned" bytes = pipeline output size at the moment of error, not input size.
			// Observed values:
			//   0B    — split at chunk boundary, no carried bytes (most common, harmless)
			//   ~4.6KB — carried bytes from a rotation split; replayed into next segment, not lost
			// At 6 Mbps, 5000B ≈ 6 ms ≈ <0.25 frame at 60 fps — imperceptible in recordings.
			if len(result) > 0 {
				log.Warnf("已丢弃 FLV 流分块：%dB", len(result))
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
				r.Stop(roomId)
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
		l.Debugf("recording not found, skip recovery")
		return
	} else if status := info.status.Load(); status == recoveringPtr {
		l.Infof("stream is recovering, skipped.")
		return
	}
	l.Infof("trying to recover stream capture...")

	info.status.Store(recoveringPtr)
	attempt := 1
	retryStart := time.Now()
	for {
		err := r.Start(roomId)
		if err == nil {
			l.Info("start live stream recovery: success")
			return
		}

		switch err {
		case ErrMaxConcurrentRecordingsReached:
			l.Infof("stop recovery due to: %v", err)
			r.Stop(roomId)
			return
		case ErrRoomEncrypted, ErrRoomBanned:
			l.Infof("stream is banned or premium, will not recover.")
			r.Stop(roomId)
			return
		default:

			// Should check if recording was manually stopped
			if _, ok := r.recording.Load(roomId); !ok {
				l.Infof("recording removed during retry, will not recover.")
				return
			}

			// if the error is stream not live, we should retry until max retry minutes reached, instead of max attempts, since the stream may be live again after some time
			if err == ErrStreamNotLive {
				// use r.cfg.MaxRetryMinutes to limit the total retry duration, instead of max attempts, since the stream may be live again after some time
				if time.Since(retryStart) >= time.Duration(r.cfg.MaxRetryMinutes)*time.Minute {
					l.Infof("stop recovery after retrying for %d minutes", r.cfg.MaxRetryMinutes)
					r.Stop(roomId)
					return
				}
			} else if attempt >= r.cfg.MaxRecoveryAttempts {
				l.Infof("maximum recovery attempts reached (%d), will not recover", r.cfg.MaxRecoveryAttempts)
				r.Stop(roomId)
				return
			} else {
				l.Warnf("recovery attempt #%d failed: %v", attempt, err)
				l.Infof("will retry stream recovery in 15 seconds...")
			}

			timer := time.NewTimer(15 * time.Second)
			select {
			case <-timer.C:
				attempt++
				continue
			case <-r.ctx.Done():
				l.Infof("service is stopping, aborting recovery")
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
	if err != nil {
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
		logger.Debug("no need to convert source to mp4, skipped")
		return
	}

	// Skip files that are already in the final .mp4 format.
	if filepath.Ext(outputPath) == ".mp4" {
		logger.Debugf("skipping finalize conversion for already-final mp4 file: %s", outputPath)
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

func (r *Service) backgroundMaintenance(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	lastActiveCount := 0

	for {
		select {
		case <-ticker.C:
			activeCount := r.recording.Size()

			if activeCount == 0 && lastActiveCount > 0 {
				// Just transitioned from active to idle - cleanup
				logger.Info("当前无进行中的录制，执行维护性 GC")

				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				logger.Debugf("Before cleanup: Alloc=%d MB, Sys=%d MB, NumGC=%d",
					m.Alloc/1024/1024, m.Sys/1024/1024, m.NumGC)

				runtime.GC()
				debug.FreeOSMemory()

				runtime.ReadMemStats(&m)
				logger.Infof("清理后：Alloc=%d MB，Sys=%d MB",
					m.Alloc/1024/1024, m.Sys/1024/1024)
			}

			lastActiveCount = activeCount

		case <-ctx.Done():
			return
		}
	}
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
