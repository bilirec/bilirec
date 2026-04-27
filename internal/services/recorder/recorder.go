package recorder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/eric2788/bilirec/internal/modules/bilibili"
	"github.com/eric2788/bilirec/internal/modules/config"
	"github.com/eric2788/bilirec/internal/processors"
	"github.com/eric2788/bilirec/internal/services/convert"
	"github.com/eric2788/bilirec/internal/services/stream"
	"github.com/eric2788/bilirec/pkg/ds"
	"github.com/eric2788/bilirec/pkg/flv"
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

var ErrMaxConcurrentRecordingsReached = errors.New("maximum concurrent recordings reached")
var ErrRecordingStarted = errors.New("recording already started")
var ErrStreamNotLive = errors.New("the room is not live streaming")
var ErrEmptyStreamURLs = errors.New("no stream urls available")
var ErrStreamURLsUnreachable = errors.New("all stream urls are unreachable")
var ErrRoomBanned = errors.New("the room is banned")
var ErrRoomEncrypted = errors.New("the room is encrypted")
var ErrInsufficientDiskSpace = errors.New("insufficient disk space")

type Service struct {
	st           *stream.Service
	cv           *convert.Service
	bilic        *bilibili.Client
	recording    *xsync.Map[int, *Info]
	writingFiles ds.Set[string]
	pipes        *xsync.Map[int, *pipeline.Pipe[[]byte]]

	cfg *config.Config
	ctx context.Context
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
	go initOutputDir(cfg)

	lc.Append(fx.StopHook(cancel))
	return s
}

func (r *Service) Start(roomId int) error {

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

	roomInfo, err := r.bilic.GetLiveRoomInfo(roomId)
	if err != nil {
		return err
	} else if roomInfo.IsEncrypted {
		return ErrRoomEncrypted
	} else if roomInfo.LockStatus != 0 {
		return ErrRoomBanned
	} else if roomInfo.LiveStatus != 1 {
		return ErrStreamNotLive
	}

	urls, err := r.bilic.GetStreamURLsV2(roomId)
	if err != nil {
		return err
	} else if len(urls) == 0 {
		return ErrEmptyStreamURLs
	}

	now := time.Now()
	ctx, cancel := context.WithCancel(r.ctx)

	// retry mechanism
	for _, url := range urls {
		resp, err := r.bilic.FetchLiveStreamUrl(url)
		if err != nil {
			l.Errorf("cannot fetch url: %v, will try next url", err)
			continue
		}
		ch, err := r.st.ReadStream(resp, ctx)
		if err != nil {
			l.Errorf("cannot capture url stream: %v, will try next url", err)
			continue
		}

		// initialize Recorder info
		info := &Info{
			cancel:    cancel,
			startTime: now,
			room:      roomInfo,
		}
		info.SetOutputPath("") // initialize output path to empty string to avoid potential nil pointer dereference in finalize()
		return r.prepare(roomId, ch, ctx, info)
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
		logger.Warnf("recording for room %d not found", roomId)
	}

	if hasPipe && !hasRecording {
		logger.Warnf("found orphaned pipe from room %d, closing it...", roomId)
		pipe.Close()
	}

	return hasRecording
}

func (r *Service) prepare(roomId int, ch <-chan []byte, ctx context.Context, info *Info) error {

	info.status.Store(recordingPtr)
	r.recording.Store(roomId, info)

	go func() {
		defer r.recover(roomId)
		defer info.cancel()
		err := r.rotate(roomId, ch, info, ctx)
		if err != nil {
			logger.Errorf("error rotating recording: %v", err)
		}
	}()

	go r.checkRecordingDurationPeriodically(roomId, ctx)
	return nil
}

func (r *Service) rotate(roomId int, ch <-chan []byte, info *Info, ctx context.Context) error {
	l := logger.WithField("room", roomId)
	segment := 0
	// Keep one fixer instance across rotation so partial/raw alignment state is preserved.
	sharedFixer := flv.NewRealtimeFixer()
	defer sharedFixer.Close()
	// videoHdr and audioHdr are nil for segment 0; populated from FlvHeaderChangedError on rotation.
	var videoHdr, audioHdr []byte
	for {

		outputPath, err := r.rotateFilePath(info, segment)
		if err != nil {
			return fmt.Errorf("cannot prepare file path: %v", err)
		} else {
			info.SetOutputPath(outputPath)
		}

		pipe := pipeline.New(
			// fix FLV stream (shared fixer across segments)
			processors.NewFlvStreamFixerWithFixer(sharedFixer),
			// detect FLV header changes (e.g. due to stream quality changes) and trigger pipe rotation
			processors.NewFlvHeaderSplitDetectorSeeded(videoHdr),
			// emit FLV file header once per segment, with optional video/audio sequence-header tags
			processors.NewFlvHeaderWriter(videoHdr, audioHdr),
			// write to file with buffered writer, flushes every 5 seconds then writes to disk
			processors.NewBufferedStreamWriter(info.OutputPath(), config.ReadOnly.LiveStreamWriterBufferSize()),
		)

		startCtx, startCancel := context.WithTimeout(ctx, 10*time.Second)
		if err := pipe.Open(startCtx); err != nil {
			startCancel()
			return fmt.Errorf("cannot open pipeline: %v", err)
		}
		startCancel()

		r.writingFiles.Add(filepath.Base(info.OutputPath()))
		r.pipes.Store(roomId, pipe)

		err = r.rev(roomId, ch, info, ctx, pipe)
		if err != nil {
			var headerChanged *flv.FlvHeaderChangedError
			if errors.As(err, &headerChanged) {
				l.Info("rotating file due to video header change detected in stream")
				videoHdr = headerChanged.VideoHeaderTag
				audioHdr = headerChanged.AudioHeaderTag
				// Before starting a new segment, reset the fixer's timestamp tracking
				// so the new segment's timestamps start from 0 instead of continuing
				// from the previous segment's time range.
				sharedFixer.ResetTimestampStore()
				segment++
				continue
			} else if err == processors.ErrNotFlvFile {
				l.Warn("stream format invalid, aborting recording")
				// Wait before aborting to prevent rapid reconnection loops (e.g. when stream URL
				// briefly returns garbage before stabilizing). This mirrors the original 5-second
				// buffer introduced in d8425df.
				timer := time.NewTimer(5 * time.Second)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
				}
				break
			} else {
				l.Errorf("error writing data to file: %v", err)
			}
		}
		break
	}
	return nil
}

func (r *Service) rev(roomId int, ch <-chan []byte, info *Info, ctx context.Context, pipe *pipeline.Pipe[[]byte]) error {
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
			logger.WithField("room", roomId).Warnf("abandoned flv stream chunk: %dB", len(result))
			return err
		}
	}
	return nil
}

func (r *Service) checkRecordingDurationPeriodically(roomId int, ctx context.Context) {
	log := logger.WithField("room", roomId)
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	maxDuration := time.Duration(r.cfg.MaxRecordingHours) * time.Hour
	for {
		select {
		case <-ticker.C:
			info, ok := r.recording.Load(roomId)
			if !ok {
				return
			}
			elapsed := time.Since(info.startTime)
			if elapsed >= maxDuration {
				log.Infof("maximum recording hours reached (%v), stopping", elapsed.Round(time.Minute))
				r.Stop(roomId)
				return
			}

			if int(elapsed.Minutes())%30 == 0 {
				remaining := maxDuration - elapsed
				log.Infof("recording: %v elapsed, %v remaining, %d MB", elapsed.Round(time.Minute), remaining.Round(time.Minute), info.bytesRead.Load()/1024/1024)
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
	l.Infof("trying to recover stream capture...")
	info, ok := r.recording.Load(roomId)
	if !ok {
		l.Infof("recording stopped manually, skipped.")
		return
	} else if status := info.status.Load(); status == recoveringPtr {
		l.Infof("stream is recovering, skipped.")
		return
	}

	info.status.Store(recoveringPtr)
	attempt := 1
	retryStart := time.Now()
	for {
		err := r.Start(roomId)
		if err == nil {
			l.Info("start live stream recovery: success")
			return
		}
		l.Errorf("recovery attempt #%d failed: %v", attempt, err)
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
			}

			l.Infof("will retry stream recovery in 15 seconds...")
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
		logger.Warnf("skipping finalize for room %d: output path is empty", roomId)
		return
	}

	defer r.writingFiles.Remove(filepath.Base(outputPath))

	fileInfo, err := os.Stat(outputPath)
	if err != nil {
		logger.Errorf("failed to stat recorded file for room %d: %v", roomId, err)
		return
	} else if fileInfo.Size() < 1024 { // less than 1KB
		logger.Warnf("recorded file for room %d is too small (%d bytes), skipping finallization and removing file", roomId, fileInfo.Size())
		if err := os.Remove(outputPath); err != nil {
			logger.Errorf("failed to remove empty file %s: %v", outputPath, err)
		}
		return
	}

	if !r.cfg.ConvertFLVToMp4 {
		logger.Debug("no need to convert flv to mp4, skipped")
		return
	}

	// process finalization via convert service
	if queue, err := r.cv.Enqueue(outputPath, "mp4", r.cfg.DeleteFlvAfterConvert); err != nil {
		logger.Errorf("failed to enqueue conversion for room %d: %v", roomId, err)
		logger.Warnf("you may need to convert mp4 manually for room: %d", roomId)
	} else {
		logger.Infof("enqueued convertion for room %d: %s", roomId, queue.TaskID)
		logger.Infof("the output path will be: %s", queue.OutputPath)
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
			var m runtime.MemStats
			runtime.ReadMemStats(&m)

			if activeCount == 0 && lastActiveCount > 0 {
				// Just transitioned from active to idle - cleanup
				logger.Info("No ongoing recordings, performing maintenance GC")
				runtime.GC()
				debug.FreeOSMemory()

				runtime.ReadMemStats(&m)
				logger.Infof("After cleanup: Alloc=%d MB, Sys=%d MB",
					m.Alloc/1024/1024, m.Sys/1024/1024)
			} else if activeCount == 0 {
				// Still idle - just log
				logger.Debugf("Idle: Memory: Alloc=%d MB, Sys=%d MB, NumGC=%d",
					m.Alloc/1024/1024, m.Sys/1024/1024, m.NumGC)
			} else {
				// Recordings active - just log stats
				logger.Debugf("Active recordings: %d, Memory: Alloc=%d MB, Sys=%d MB, NumGC=%d",
					activeCount, m.Alloc/1024/1024, m.Sys/1024/1024, m.NumGC)
			}

			lastActiveCount = activeCount

		case <-ctx.Done():
			return
		}
	}
}

// the time should be the time you start the record, not live start
func (r *Service) rotateFilePath(info *Info, segment int) (string, error) {
	dirPath := fmt.Sprintf("%s/%s-%d", r.cfg.OutputDir, info.room.Uname, info.room.RoomID)
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return "", err
	}
	safeTitle := utils.TruncateString(utils.SanitizeFilename(info.room.Title), 20)
	if segment == 0 {
		return fmt.Sprintf("%s/%s-%s.flv", dirPath, safeTitle, info.startTime.Format("20060102_150405")), nil
	} else {
		return fmt.Sprintf("%s/%s-%s-%d.flv", dirPath, safeTitle, info.startTime.Format("20060102_150405"), segment), nil
	}
}

func initOutputDir(cfg *config.Config) {
	if err := os.MkdirAll(cfg.OutputDir, 0755); err != nil {
		logger.Fatalf("cannot create output directory %s: %v", cfg.OutputDir, err)
	}
}
