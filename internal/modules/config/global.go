package config

var ReadOnly *GlobalReadOnly = nil

const (
	defaultCloudConvertCheckIntervalSecs                 = 180
	defaultCloudConvertMaxConcurrentDownloads            = 1
	defaultFFmpegCheckIntervalSecs                       = 60
	defaultFFmpegMaxConcurrentTasks                      = 1
	defaultFFmpegAllowDuringRecordingMaxActiveRecordings = 1
	defaultLiveStreamWriterSyncPeriodSecs                = 0 // Disable periodic sync to reduce SD card wear; data syncs only on Close()
	defaultLiveStreamWriterFlushPeriodSecs               = 15
	defaultLiveStreamWriterChanBufferSize                = 64
	defaultLiveStreamWriterBytesPoolSize                 = 512 * 1024 // 512KB per buffer
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

func (g *GlobalReadOnly) SkipSmallFlush() bool {
	return g.config.skipSmallFlush
}

func (g *GlobalReadOnly) SequentialWrite() bool {
	return g.config.sequentialWrite
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

func (g *GlobalReadOnly) Validate() {
	if g.config.CloudConvertCheckIntervalSecs <= 0 {
		logger.Warnf("CLOUDCONVERT_CHECK_INTERVAL_SECS is invalid (%d), using default %d seconds", g.config.CloudConvertCheckIntervalSecs, defaultCloudConvertCheckIntervalSecs)
	}
	if g.config.CloudConvertMaxConcurrentDownloads <= 0 {
		logger.Warnf("CLOUDCONVERT_MAX_CONCURRENT_DOWNLOADS is invalid (%d), using default %d", g.config.CloudConvertMaxConcurrentDownloads, defaultCloudConvertMaxConcurrentDownloads)
	}
	if g.config.FFmpegCheckIntervalSecs <= 0 {
		logger.Warnf("FFMPEG_CHECK_INTERVAL_SECS is invalid (%d), using default %d seconds", g.config.FFmpegCheckIntervalSecs, defaultFFmpegCheckIntervalSecs)
	}
	if g.config.FFmpegMaxConcurrentTasks <= 0 {
		logger.Warnf("FFMPEG_MAX_CONCURRENT_TASKS is invalid (%d), using default %d", g.config.FFmpegMaxConcurrentTasks, defaultFFmpegMaxConcurrentTasks)
	}
	if g.config.FFmpegAllowDuringRecordingMaxActiveRecordings < 1 {
		logger.Debugf("FFMPEG_ALLOW_DURING_RECORDING_MAX_ACTIVE_RECORDINGS is %d, threshold disabled (no active-recordings limit)", g.config.FFmpegAllowDuringRecordingMaxActiveRecordings)
	}
	if g.config.liveStreamWriterSyncPeriod < 0 {
		logger.Warnf("LIVE_STREAM_WRITER_SYNC_PERIOD_SECS is invalid (%d), using default %d seconds", g.config.liveStreamWriterSyncPeriod, defaultLiveStreamWriterSyncPeriodSecs)
	}
	if g.config.liveStreamWriterFlushPeriod <= 0 {
		logger.Warnf("LIVE_STREAM_WRITER_FLUSH_PERIOD_SECS is invalid (%d), using default %d seconds", g.config.liveStreamWriterFlushPeriod, defaultLiveStreamWriterFlushPeriodSecs)
	}
	// Reject protocol-mismatch configs between FRP backend mode and Fiber listener mode.
	if g.config.ServerCrt != "" && g.config.ServerKey != "" && g.config.FRPEnabled && !g.config.FRPHttps {
		logger.Fatalf("invalid config: SERVER_CRT and SERVER_KEY enable HTTPS-only server, but FRP_ENABLED=true with FRP_HTTPS=false configures an HTTP FRP backend; this protocol mismatch causes FRP to fail. Set FRP_HTTPS=true, or disable HTTPS certs (SERVER_CRT/SERVER_KEY), or disable FRP")
	}
	if g.config.FRPEnabled && g.config.FRPHttps && (g.config.ServerCrt == "" || g.config.ServerKey == "") {
		logger.Fatalf("invalid config: FRP_ENABLED=true with FRP_HTTPS=true requires Fiber HTTPS to be enabled by setting both SERVER_CRT and SERVER_KEY; otherwise FRP HTTPS backend protocol mismatches and FRP fails. Set SERVER_CRT and SERVER_KEY, or set FRP_HTTPS=false, or disable FRP")
	}
}
