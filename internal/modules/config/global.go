package config

var ReadOnly *GlobalReadOnly = nil

const (
	defaultCloudConvertCheckIntervalSecs      = 180
	defaultCloudConvertMaxConcurrentDownloads = 1
	defaultFFmpegCheckIntervalSecs            = 60
	defaultFFmpegMaxConcurrentTasks           = 1
	defaultLiveStreamWriterSyncPeriodSecs     = 45
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
	if g.config.liveStreamWriterSyncPeriod <= 0 {
		return defaultLiveStreamWriterSyncPeriodSecs
	}
	return g.config.liveStreamWriterSyncPeriod
}

func (g *GlobalReadOnly) SkipSmallFlush() bool {
	return g.config.skipSmallFlush
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
	if g.config.liveStreamWriterSyncPeriod <= 0 {
		logger.Warnf("LIVE_STREAM_WRITER_SYNC_PERIOD_SECS is invalid (%d), using default %d seconds", g.config.liveStreamWriterSyncPeriod, defaultLiveStreamWriterSyncPeriodSecs)
	}
}
