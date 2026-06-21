package config

import "fmt"

var ReadOnly *GlobalReadOnly = nil

const (
	defaultCloudConvertCheckIntervalSecs                 = 180
	defaultCloudConvertMaxConcurrentDownloads            = 1
	defaultFFmpegCheckIntervalSecs                       = 60
	defaultFFmpegMaxConcurrentTasks                      = 1
	defaultFFmpegAllowDuringRecordingMaxActiveRecordings = 1
	defaultLiveStreamWriterSyncPeriodSecs                = 0 // Disable periodic sync to reduce SD card wear; data syncs only on Close()
	defaultLiveStreamWriterColdCacheReleaseSecs          = 60
	defaultLiveStreamWriterFlushPeriodSecs               = 15
	defaultLiveStreamWriterChanBufferSize                = 64
	defaultLiveStreamWriterBytesPoolSize                 = 512 * 1024 // 512KB per buffer
	defaultReadStreamBytesPoolSize                       = 512 * 1024
	defaultReadStreamBytesPoolSizeHigh                   = 1024 * 1024
	defaultReadStreamChanBufferSizeHigh                  = 48
	defaultLiveStreamWriterBytesPoolSizeHigh             = 1024 * 1024
	defaultSkipSmallFlushThreshold                       = 1 * 1024 * 1024
)

// for global readonly access
type GlobalReadOnly struct {
	config *Config
}

func (g *GlobalReadOnly) UploadBufferSize() int {
	return g.config.uploadBufferSize
}

func (g *GlobalReadOnly) DownloadBufferSize() int {
	return g.config.downloadBufferSize
}

func (g *GlobalReadOnly) DownloadWriterBufferSize() int {
	return g.config.streamWriterBufferSize
}

func (g *GlobalReadOnly) LiveStreamWriterBufferSize() int {
	return g.config.liveStreamWriterBufferSize
}

func (g *GlobalReadOnly) LiveStreamWriterSyncPeriodSecs() int {
	if g.config.liveStreamWriterSyncPeriod < 0 {
		return defaultLiveStreamWriterSyncPeriodSecs
	}
	return g.config.liveStreamWriterSyncPeriod
}

func (g *GlobalReadOnly) LiveStreamWriterColdCacheReleaseSecs() int {
	if g.LiveStreamWriterSyncPeriodSecs() > 0 {
		return 0
	}
	if g.config.liveStreamWriterColdCacheReleasePeriod < 0 {
		return 0
	}
	if g.config.liveStreamWriterColdCacheReleasePeriod == 0 {
		return 0
	}
	return g.config.liveStreamWriterColdCacheReleasePeriod
}

func (g *GlobalReadOnly) LiveStreamWriterFlushPeriodSecs() int {
	if g.config.liveStreamWriterFlushPeriod <= 0 {
		return defaultLiveStreamWriterFlushPeriodSecs
	}
	return g.config.liveStreamWriterFlushPeriod
}

func (g *GlobalReadOnly) LiveStreamWriterChanBufferSize() int {
	if g.config.liveStreamWriterChanBufferSize <= 0 {
		return defaultLiveStreamWriterChanBufferSize
	}
	return g.config.liveStreamWriterChanBufferSize
}

func (g *GlobalReadOnly) LiveStreamWriterBytesPoolSize() int {
	if g.config.liveStreamWriterBytesPoolSize <= 0 {
		return defaultLiveStreamWriterBytesPoolSize
	}
	return g.config.liveStreamWriterBytesPoolSize
}

func (g *GlobalReadOnly) ReadStreamBytesPoolSize() int {
	if g.config.ReadStreamBytesPoolSize <= 0 {
		return defaultReadStreamBytesPoolSize
	}
	return g.config.ReadStreamBytesPoolSize
}

func (g *GlobalReadOnly) ReadStreamBytesPoolSizeHigh() int {
	if g.config.readStreamBytesPoolSizeHigh <= 0 {
		return defaultReadStreamBytesPoolSizeHigh
	}
	return g.config.readStreamBytesPoolSizeHigh
}

func (g *GlobalReadOnly) ReadStreamChanBufferSizeHigh() int {
	if g.config.readStreamChanBufferSizeHigh <= 0 {
		return defaultReadStreamChanBufferSizeHigh
	}
	return g.config.readStreamChanBufferSizeHigh
}

func (g *GlobalReadOnly) LiveStreamWriterBytesPoolSizeHigh() int {
	if g.config.liveStreamWriterBytesPoolSizeHigh <= 0 {
		return defaultLiveStreamWriterBytesPoolSizeHigh
	}
	return g.config.liveStreamWriterBytesPoolSizeHigh
}

func (g *GlobalReadOnly) SkipSmallFlushThreshold() int {
	if g.config.skipSmallFlushThreshold <= 0 {
		return defaultSkipSmallFlushThreshold
	}
	return g.config.skipSmallFlushThreshold
}

func (g *GlobalReadOnly) SkipSmallFlush() bool {
	return g.config.skipSmallFlush
}

func (g *GlobalReadOnly) SequentialWrite() bool {
	return g.config.sequentialWrite
}

func (g *GlobalReadOnly) DropFilePageCache() bool {
	return g.config.dropFilePageCache
}

func (g *GlobalReadOnly) RestAuthEnabled() bool {
	return g.config.Username != "" && g.config.PasswordHash != ""
}

func (g *GlobalReadOnly) ViewerEnabled() bool {
	return g.RestAuthEnabled() && g.config.ViewerUsername != "" && g.config.ViewerPasswordHash != ""
}

func (g *GlobalReadOnly) CloudConvertCheckIntervalSecs() int {
	if g.config.CloudConvertCheckIntervalSecs <= 0 {
		return defaultCloudConvertCheckIntervalSecs
	}
	return g.config.CloudConvertCheckIntervalSecs
}

func (g *GlobalReadOnly) CloudConvertMaxConcurrentDownloads() int {
	if g.config.CloudConvertMaxConcurrentDownloads <= 0 {
		return defaultCloudConvertMaxConcurrentDownloads
	}
	return g.config.CloudConvertMaxConcurrentDownloads
}

func (g *GlobalReadOnly) CloudConvertAllowDuringRecording() bool {
	return g.config.CloudConvertAllowDuringRecording
}

// CloudConvertAllowDuringRecordingMaxActiveRecordings controls when cloudconvert is allowed
// during active recording, but only when CLOUDCONVERT_ALLOW_DURING_RECORDING=true.
// Returns <1 when threshold is disabled (no active-recordings limit).
func (g *GlobalReadOnly) CloudConvertAllowDuringRecordingMaxActiveRecordings() int {
	return g.config.CloudConvertAllowDuringRecordingMaxActiveRecordings
}

func (g *GlobalReadOnly) FFmpegCheckIntervalSecs() int {
	if g.config.FFmpegCheckIntervalSecs <= 0 {
		return defaultFFmpegCheckIntervalSecs
	}
	return g.config.FFmpegCheckIntervalSecs
}

func (g *GlobalReadOnly) FFmpegMaxConcurrentTasks() int {
	if g.config.FFmpegMaxConcurrentTasks <= 0 {
		return defaultFFmpegMaxConcurrentTasks
	}
	return g.config.FFmpegMaxConcurrentTasks
}

func (g *GlobalReadOnly) FFmpegAllowDuringRecording() bool {
	return g.config.FFmpegAllowDuringRecording
}

// FFmpegAllowDuringRecordingMaxActiveRecordings controls when ffmpeg is allowed
// during active recording, but only when FFMPEG_ALLOW_DURING_RECORDING=true.
// Returns <1 when threshold is disabled (no active-recordings limit).
func (g *GlobalReadOnly) FFmpegAllowDuringRecordingMaxActiveRecordings() int {
	return g.config.FFmpegAllowDuringRecordingMaxActiveRecordings
}

func (g *GlobalReadOnly) Validate() error {
	switch g.config.BilibiliLoginMode {
	case "startup", "controller", "anonymous":
	default:
		return fmt.Errorf("配置无效：BILIBILI_LOGIN_MODE 仅支持 startup、controller、anonymous，当前值：%s", g.config.BilibiliLoginMode)
	}
	if g.config.CloudConvertCheckIntervalSecs <= 0 {
		logger.Warnf("CLOUDCONVERT_CHECK_INTERVAL_SECS 无效（%d），使用默认值 %d 秒", g.config.CloudConvertCheckIntervalSecs, defaultCloudConvertCheckIntervalSecs)
	}
	if g.config.CloudConvertMaxConcurrentDownloads <= 0 {
		logger.Warnf("CLOUDCONVERT_MAX_CONCURRENT_DOWNLOADS 无效（%d），使用默认值 %d", g.config.CloudConvertMaxConcurrentDownloads, defaultCloudConvertMaxConcurrentDownloads)
	}
	if g.config.CloudConvertAllowDuringRecordingMaxActiveRecordings < 1 {
		logger.Debugf("CLOUDCONVERT_ALLOW_DURING_RECORDING_MAX_ACTIVE_RECORDINGS is %d, threshold disabled (no active-recordings limit)", g.config.CloudConvertAllowDuringRecordingMaxActiveRecordings)
	}
	if g.config.FFmpegCheckIntervalSecs <= 0 {
		logger.Warnf("FFMPEG_CHECK_INTERVAL_SECS 无效（%d），使用默认值 %d 秒", g.config.FFmpegCheckIntervalSecs, defaultFFmpegCheckIntervalSecs)
	}
	if g.config.FFmpegMaxConcurrentTasks <= 0 {
		logger.Warnf("FFMPEG_MAX_CONCURRENT_TASKS 无效（%d），使用默认值 %d", g.config.FFmpegMaxConcurrentTasks, defaultFFmpegMaxConcurrentTasks)
	}
	if g.config.FFmpegAllowDuringRecordingMaxActiveRecordings < 1 {
		logger.Debugf("FFMPEG_ALLOW_DURING_RECORDING_MAX_ACTIVE_RECORDINGS is %d, threshold disabled (no active-recordings limit)", g.config.FFmpegAllowDuringRecordingMaxActiveRecordings)
	}
	if g.config.liveStreamWriterSyncPeriod < 0 {
		logger.Warnf("LIVE_STREAM_WRITER_SYNC_PERIOD_SECS 无效（%d），使用默认值 %d 秒", g.config.liveStreamWriterSyncPeriod, defaultLiveStreamWriterSyncPeriodSecs)
	}
	if g.config.liveStreamWriterColdCacheReleasePeriod < 0 {
		logger.Warnf("LIVE_STREAM_WRITER_COLD_CACHE_RELEASE_SECS 无效（%d），冷缓存释放已关闭", g.config.liveStreamWriterColdCacheReleasePeriod)
	}
	if g.config.liveStreamWriterSyncPeriod > 0 && g.config.liveStreamWriterColdCacheReleasePeriod > 0 {
		logger.Warnf(
			"LIVE_STREAM_WRITER_SYNC_PERIOD_SECS（%d）与 LIVE_STREAM_WRITER_COLD_CACHE_RELEASE_SECS（%d）同时启用；以 periodic fsync 为准，冷缓存释放已关闭",
			g.config.liveStreamWriterSyncPeriod,
			g.config.liveStreamWriterColdCacheReleasePeriod,
		)
	}
	if g.config.liveStreamWriterFlushPeriod <= 0 {
		logger.Warnf("LIVE_STREAM_WRITER_FLUSH_PERIOD_SECS 无效（%d），使用默认值 %d 秒", g.config.liveStreamWriterFlushPeriod, defaultLiveStreamWriterFlushPeriodSecs)
	}
	if g.config.skipSmallFlushThreshold <= 0 {
		logger.Warnf("SKIP_SMALL_FLUSH_THRESHOLD 无效（%d），使用默认值 %d 字节", g.config.skipSmallFlushThreshold, defaultSkipSmallFlushThreshold)
	}
	if g.config.readStreamBytesPoolSizeHigh <= 0 {
		logger.Warnf("READ_STREAM_BYTES_POOL_SIZE_HIGH 无效（%d），使用默认值 %d 字节", g.config.readStreamBytesPoolSizeHigh, defaultReadStreamBytesPoolSizeHigh)
	}
	if g.config.readStreamChanBufferSizeHigh <= 0 {
		logger.Warnf("READ_STREAM_CHAN_BUFFER_SIZE_HIGH 无效（%d），使用默认值 %d", g.config.readStreamChanBufferSizeHigh, defaultReadStreamChanBufferSizeHigh)
	}
	if g.config.liveStreamWriterBytesPoolSizeHigh <= 0 {
		logger.Warnf("LIVE_STREAM_WRITER_BYTES_POOL_SIZE_HIGH 无效（%d），使用默认值 %d 字节", g.config.liveStreamWriterBytesPoolSizeHigh, defaultLiveStreamWriterBytesPoolSizeHigh)
	}
	// Reject protocol-mismatch configs between FRP backend mode and Fiber listener mode.
	if g.config.ServerCrt != "" && g.config.ServerKey != "" && g.config.FRPEnabled && !g.config.FRPHttps {
		return fmt.Errorf("配置无效：SERVER_CRT 和 SERVER_KEY 启用仅 HTTPS 服务器，但 FRP_ENABLED=true 且 FRP_HTTPS=false 会将 FRP 后端配置为 HTTP；协议不匹配会导致 FRP 失败。请设置 FRP_HTTPS=true，或禁用 HTTPS 证书（SERVER_CRT/SERVER_KEY），或禁用 FRP")
	}
	if g.config.FRPEnabled && g.config.FRPHttps && (g.config.ServerCrt == "" || g.config.ServerKey == "") {
		return fmt.Errorf("配置无效：当 FRP_ENABLED=true 且 FRP_HTTPS=true 时，必须同时设置 SERVER_CRT 和 SERVER_KEY 以启用 Fiber HTTPS；否则 FRP HTTPS 后端协议不匹配会导致 FRP 失败。请设置 SERVER_CRT 和 SERVER_KEY，或将 FRP_HTTPS=false，或禁用 FRP")
	}
	return nil
}
