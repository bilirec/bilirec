package config

import (
	"context"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/utils"
	"go.uber.org/fx"
	"golang.org/x/crypto/bcrypt"
)

// FRP token injected at build-time via ldflags; defaults to empty.
// When empty, FRPToken remains empty unless overridden by FRP_TOKEN env var.
var frpTokenInjected = ""

const (
	officialFRPServer = "tunnel.bilirec.org:7000"
	officialFRPDomain = "tunnel.bilirec.org"
)

// all config will be loaded from environment variables
type Config struct {
	BilibiliLoginMode string // startup, controller, or anonymous (default: controller)
	Host              string
	Port              string
	TrustedProxies    []string

	MetricsEnabled bool
	MetricsHost    string
	MetricsPort    string

	VictoriaLogsEnabled      bool
	VictoriaLogsURL          string
	VictoriaLogsStreamFields string
	VictoriaLogsAccountID    string
	VictoriaLogsProjectID    string
	VictoriaLogsTimeout      time.Duration

	FRPEnabled     bool
	FRPServer      string
	FRPToken       string
	FRPBaseDomain  string
	FRPHttps       bool
	FRPSchemeHttps bool

	MaxConcurrentRecordings   int
	MaxRecordingHours         int
	MaxRecoveryAttempts       int
	MaxRetryMinutes           int
	RecordingRecoveryDuration string // preserve or reset (default: preserve)

	OutputDir   string
	SecretDir   string
	DatabaseDir string

	ConvertToMp4             bool
	DeleteSourceAfterConvert bool
	NoConvertIfInvalid       bool

	// DanmakuOutputFormat selects the sidecar file format: jsonl or xml (default: jsonl).
	// Whether to record danmaku is per start option / room config (RecordDanmaku),
	// not a global switch; the bilibili danmaku protocol capability itself is always
	// available for other consumers (e.g. future websocket-based subcheck).
	DanmakuOutputFormat string
	// DanmakuOverflowPolicy controls the per-room message channel when full:
	// "drop" (default; never blocks the websocket) or "block" (wait for space;
	// prefer completeness, but a stalled disk may stall the danmaku connection).
	DanmakuOverflowPolicy string

	CloudConvertThreshold int64
	CloudConvertApiKey    string

	PublicBaseUrl      string
	FrontendURL        *url.URL
	WebPushSubscriber  string
	NotifySSEToken     string
	Username           string
	PasswordHash       string
	ViewerUsername     string
	ViewerPasswordHash string
	JwtSecret          string

	ServerCrt string
	ServerKey string

	Debug           bool
	ProductionMode  bool
	SilentAccessLog bool

	MinDiskSpaceBytes int64

	CloudConvertCheckIntervalSecs                       int
	CloudConvertMaxConcurrentDownloads                  int
	CloudConvertAllowDuringRecording                    bool
	CloudConvertAllowDuringRecordingMaxActiveRecordings int
	FFmpegCheckIntervalSecs                             int
	FFmpegMaxConcurrentTasks                            int
	FFmpegAllowDuringRecording                          bool
	FFmpegAllowDuringRecordingMaxActiveRecordings       int
	SubcheckRoomsPerShard                               int
	SubcheckTickSecs                                    int
	SubcheckMinIntervalSecs                             int
	SubcheckMaxIntervalSecs                             int
	SubcheckMaxShards                                   int
	SubcheckJitterSecs                                  int

	// configurable performances
	ReadStreamBytesPoolSize      int
	ReadStreamChanBufferSize     int
	readStreamBytesPoolSizeHigh  int
	readStreamChanBufferSizeHigh int

	// configurable global performances
	uploadBufferSize                       int
	downloadBufferSize                     int
	streamWriterBufferSize                 int
	liveStreamWriterBufferSize             int
	liveStreamWriterSyncPeriod             int
	liveStreamWriterColdCacheReleasePeriod int
	liveStreamWriterFlushPeriod            int
	liveStreamWriterChanBufferSize         int
	liveStreamWriterBytesPoolSize          int
	liveStreamWriterBytesPoolSizeHigh      int
	skipSmallFlushThreshold                int
	skipSmallFlush                         bool
	sequentialWrite                        bool
	dropFilePageCache                      bool

	// danmaku recording performances
	danmakuWriterBufferSize            int
	danmakuWriterMinPeriodicFlushBytes int
	danmakuWriterFlushPeriod           int
	danmakuWriterSyncPeriod            int
	danmakuChanBufferSize              int
	danmakuBytesPoolSize               int
}

var log = logger.Named("config")

func provider(lc fx.Lifecycle) (*Config, error) {

	// parse username and password
	username, passwordHash, err := parseUsernameAndPassword("USERNAME", "PASSWORD")
	if err != nil {
		return nil, err
	}

	// parse viewer username and password
	viewerUsername, viewerPasswordHash, viewerErr := parseUsernameAndPassword("VIEWER_USERNAME", "VIEWER_PASSWORD")
	if viewerErr != nil {
		return nil, viewerErr
	}

	// parse frontend url
	url, err := url.Parse(utils.EmptyOrElse(os.Getenv("FRONTEND_URL"), "http://localhost:8080"))

	if err != nil {
		return nil, err
	}

	// parse debug

	debug := os.Getenv("DEBUG") == "true"

	if debug {
		logger.SetDebug(true)
	}

	// Prefer the new max-active-recordings variable name. Fallback to legacy
	// "...BELOW_ACTIVE_RECORDINGS" for backward compatibility.
	ffmpegAllowDuringRecordingMaxActives := utils.EmptyOrElse(
		os.Getenv("FFMPEG_ALLOW_DURING_RECORDING_MAX_ACTIVE_RECORDINGS"),
		utils.EmptyOrElse(os.Getenv("FFMPEG_ALLOW_DURING_RECORDING_BELOW_ACTIVE_RECORDINGS"), "1"),
	)

	frpServer := utils.EmptyOrElse(os.Getenv("FRP_SERVER"), officialFRPServer)
	frpBaseDomain := utils.EmptyOrElse(os.Getenv("FRP_BASE_DOMAIN"), officialFRPDomain)
	frpToken := resolveFRPToken(frpServer, frpBaseDomain)

	c := &Config{
		BilibiliLoginMode:       strings.ToLower(strings.TrimSpace(utils.EmptyOrElse(os.Getenv("BILIBILI_LOGIN_MODE"), "controller"))),
		Host:                    strings.TrimSpace(os.Getenv("HOST")),
		Port:                    utils.EmptyOrElse(os.Getenv("PORT"), "8080"),
		TrustedProxies:          parseCommaSeparatedValues(utils.EmptyOrElse(os.Getenv("TRUSTED_PROXIES"), "161.33.159.26")),
		MetricsEnabled:          os.Getenv("METRICS_ENABLED") == "true",
		MetricsHost:             strings.TrimSpace(os.Getenv("METRICS_HOST")),
		MetricsPort:             utils.EmptyOrElse(os.Getenv("METRICS_PORT"), "9090"),
		VictoriaLogsEnabled:     os.Getenv("VICTORIALOGS_ENABLED") == "true",
		VictoriaLogsURL:         strings.TrimSpace(os.Getenv("VICTORIALOGS_URL")),
		VictoriaLogsStreamFields: utils.EmptyOrElse(strings.TrimSpace(os.Getenv("VICTORIALOGS_STREAM_FIELDS")), "app,logger"),
		VictoriaLogsAccountID:   strings.TrimSpace(os.Getenv("VICTORIALOGS_ACCOUNT_ID")),
		VictoriaLogsProjectID:   strings.TrimSpace(os.Getenv("VICTORIALOGS_PROJECT_ID")),
		VictoriaLogsTimeout:     parseDurationOrDefault(os.Getenv("VICTORIALOGS_TIMEOUT"), 10*time.Second),
		FRPEnabled:              os.Getenv("FRP_ENABLED") == "true",
		FRPServer:               frpServer,
		FRPToken:                frpToken,
		FRPBaseDomain:           frpBaseDomain,
		FRPHttps:                os.Getenv("FRP_HTTPS") == "true",
		FRPSchemeHttps:          utils.EmptyOrElse(os.Getenv("FRP_SCHEME_HTTPS"), "true") == "true",
		MaxConcurrentRecordings: utils.MustAtoi(utils.EmptyOrElse(os.Getenv("MAX_CONCURRENT_RECORDINGS"), "3")),
		MaxRecordingHours:       utils.MustAtoi(utils.EmptyOrElse(os.Getenv("MAX_RECORDING_HOURS"), "5")),
		MaxRecoveryAttempts:     utils.MustAtoi(utils.EmptyOrElse(os.Getenv("MAX_RECOVERY_ATTEMPTS"), "15")),
		MaxRetryMinutes:         utils.MustAtoi(utils.EmptyOrElse(os.Getenv("MAX_RETRY_MINUTES"), "10")),
		RecordingRecoveryDuration: strings.ToLower(strings.TrimSpace(
			utils.EmptyOrElse(os.Getenv("RECORDING_RECOVERY_DURATION"), "preserve"),
		)),
		OutputDir:                          utils.EmptyOrElse(os.Getenv("OUTPUT_DIR"), "records"),
		SecretDir:                          utils.EmptyOrElse(os.Getenv("SECRET_DIR"), "secrets"),
		DatabaseDir:                        utils.EmptyOrElse(os.Getenv("DATABASE_DIR"), "database"),
		CloudConvertThreshold:              utils.MustAtoi64(utils.EmptyOrElse(os.Getenv("CLOUDCONVERT_THRESHOLD"), "1073741824")), // 1 GB
		CloudConvertApiKey:                 os.Getenv("CLOUDCONVERT_API_KEY"),                                                      // empty to disable
		ConvertToMp4:                       os.Getenv("CONVERT_TO_MP4") == "true" || os.Getenv("CONVERT_FLV_TO_MP4") == "true",
		NoConvertIfInvalid:                 os.Getenv("NO_CONVERT_IF_INVALID") == "true",
		DeleteSourceAfterConvert:           os.Getenv("DELETE_SOURCE_AFTER_CONVERT") == "true" || os.Getenv("DELETE_FLV_AFTER_CONVERT") == "true",
		DanmakuOutputFormat:                strings.ToLower(strings.TrimSpace(utils.EmptyOrElse(os.Getenv("DANMAKU_OUTPUT_FORMAT"), "jsonl"))),
		DanmakuOverflowPolicy:              strings.ToLower(strings.TrimSpace(utils.EmptyOrElse(os.Getenv("DANMAKU_OVERFLOW_POLICY"), "drop"))),
		FrontendURL:                        url,
		PublicBaseUrl:                      utils.EmptyOrElse(os.Getenv("PUBLIC_BASE_URL"), utils.EmptyOrElse(os.Getenv("BACKEND_HOST"), "")),
		WebPushSubscriber:                  utils.EmptyOrElse(os.Getenv("WEBPUSH_SUBSCRIBER"), "mailto:webpush@example.com"),
		NotifySSEToken:                     strings.TrimSpace(os.Getenv("NOTIFY_SSE_TOKEN")),
		Username:                           username,
		PasswordHash:                       string(passwordHash),
		ViewerUsername:                     viewerUsername,
		ViewerPasswordHash:                 string(viewerPasswordHash),
		JwtSecret:                          utils.EmptyOrElse(os.Getenv("JWT_SECRET"), "bilirec_secret"),
		ServerCrt:                          os.Getenv("SERVER_CRT"),
		ServerKey:                          os.Getenv("SERVER_KEY"),
		Debug:                              debug,
		ProductionMode:                     os.Getenv("PRODUCTION_MODE") == "true",
		SilentAccessLog:                    os.Getenv("SILENT_ACCESS_LOG") == "true",
		MinDiskSpaceBytes:                  utils.MustAtoi64(utils.EmptyOrElse(os.Getenv("MIN_DISK_SPACE_BYTES"), "5368709120")), // 5GB
		CloudConvertCheckIntervalSecs:      utils.MustAtoi(utils.EmptyOrElse(os.Getenv("CLOUDCONVERT_CHECK_INTERVAL_SECS"), "180")),
		CloudConvertMaxConcurrentDownloads: utils.MustAtoi(utils.EmptyOrElse(os.Getenv("CLOUDCONVERT_MAX_CONCURRENT_DOWNLOADS"), "1")),
		CloudConvertAllowDuringRecording:   os.Getenv("CLOUDCONVERT_ALLOW_DURING_RECORDING") == "true",
		CloudConvertAllowDuringRecordingMaxActiveRecordings: utils.MustAtoi(utils.EmptyOrElse(os.Getenv("CLOUDCONVERT_ALLOW_DURING_RECORDING_MAX_ACTIVE_RECORDINGS"), "1")), // <1 = no limit; when >=1, cloudconvert during recording runs only if active recordings <= this value
		FFmpegCheckIntervalSecs:                             utils.MustAtoi(utils.EmptyOrElse(os.Getenv("FFMPEG_CHECK_INTERVAL_SECS"), "60")),
		FFmpegMaxConcurrentTasks:                            utils.MustAtoi(utils.EmptyOrElse(os.Getenv("FFMPEG_MAX_CONCURRENT_TASKS"), "1")),
		FFmpegAllowDuringRecording:                          os.Getenv("FFMPEG_ALLOW_DURING_RECORDING") == "true",
		FFmpegAllowDuringRecordingMaxActiveRecordings:       utils.MustAtoi(ffmpegAllowDuringRecordingMaxActives), // <1 = no limit; when >=1, ffmpeg during recording runs only if active recordings <= this value
		SubcheckRoomsPerShard:                               utils.MustAtoi(utils.EmptyOrElse(os.Getenv("SUBCHECK_ROOMS_PER_SHARD"), "50")),
		SubcheckTickSecs:                                    utils.MustAtoi(utils.EmptyOrElse(os.Getenv("SUBCHECK_TICK_SECS"), "10")),
		SubcheckMinIntervalSecs:                             utils.MustAtoi(utils.EmptyOrElse(os.Getenv("SUBCHECK_MIN_INTERVAL_SECS"), "60")),
		SubcheckMaxIntervalSecs:                             utils.MustAtoi(utils.EmptyOrElse(os.Getenv("SUBCHECK_MAX_INTERVAL_SECS"), "300")),
		SubcheckMaxShards:                                   utils.MustAtoi(utils.EmptyOrElse(os.Getenv("SUBCHECK_MAX_SHARDS"), "32")),
		SubcheckJitterSecs:                                  utils.MustAtoi(utils.EmptyOrElse(os.Getenv("SUBCHECK_JITTER_SECS"), "2")),

		// stream performance configs
		ReadStreamBytesPoolSize:      utils.MustAtoi(utils.EmptyOrElse(os.Getenv("READ_STREAM_BYTES_POOL_SIZE"), "524288")),       // default 512KB
		ReadStreamChanBufferSize:     utils.MustAtoi(utils.EmptyOrElse(os.Getenv("READ_STREAM_CHAN_BUFFER_SIZE"), "16")),          // default 16
		readStreamBytesPoolSizeHigh:  utils.MustAtoi(utils.EmptyOrElse(os.Getenv("READ_STREAM_BYTES_POOL_SIZE_HIGH"), "1048576")), // default 1MB for 2K/4K
		readStreamChanBufferSizeHigh: utils.MustAtoi(utils.EmptyOrElse(os.Getenv("READ_STREAM_CHAN_BUFFER_SIZE_HIGH"), "48")),     // default 48 for 2K/4K

		// global performance configs
		uploadBufferSize:                       utils.MustAtoi(utils.EmptyOrElse(os.Getenv("UPLOAD_BUFFER_SIZE"), "5242880")),                      // default 5MB
		downloadBufferSize:                     utils.MustAtoi(utils.EmptyOrElse(os.Getenv("DOWNLOAD_BUFFER_SIZE"), "5242880")),                    // default 5MB
		streamWriterBufferSize:                 utils.MustAtoi(utils.EmptyOrElse(os.Getenv("STREAM_WRITER_BUFFER_SIZE"), "1048576")),               // default 1MB
		liveStreamWriterBufferSize:             utils.MustAtoi(utils.EmptyOrElse(os.Getenv("LIVE_STREAM_WRITER_BUFFER_SIZE"), "8388608")),          // 8MB: prioritize lower flush frequency for SD card longevity
		liveStreamWriterSyncPeriod:             utils.MustAtoi(utils.EmptyOrElse(os.Getenv("LIVE_STREAM_WRITER_SYNC_PERIOD_SECS"), "0")),           // 0 = disabled; sync only on Close() to minimize SD card wear
		liveStreamWriterColdCacheReleasePeriod: utils.MustAtoi(utils.EmptyOrElse(os.Getenv("LIVE_STREAM_WRITER_COLD_CACHE_RELEASE_SECS"), "60")),   // periodic cold page-cache release during recording
		liveStreamWriterFlushPeriod:            utils.MustAtoi(utils.EmptyOrElse(os.Getenv("LIVE_STREAM_WRITER_FLUSH_PERIOD_SECS"), "15")),         // default 15s: fewer flush operations, lower SD card wear
		liveStreamWriterChanBufferSize:         utils.MustAtoi(utils.EmptyOrElse(os.Getenv("LIVE_STREAM_WRITER_CHAN_BUFFER_SIZE"), "64")),          // default 64: limits in-flight memory while still tolerating write latency bursts
		liveStreamWriterBytesPoolSize:          utils.MustAtoi(utils.EmptyOrElse(os.Getenv("LIVE_STREAM_WRITER_BYTES_POOL_SIZE"), "524288")),       // 512KB per buffer
		liveStreamWriterBytesPoolSizeHigh:      utils.MustAtoi(utils.EmptyOrElse(os.Getenv("LIVE_STREAM_WRITER_BYTES_POOL_SIZE_HIGH"), "1048576")), // default 1MB for 2K/4K
		skipSmallFlushThreshold:                utils.MustAtoi(utils.EmptyOrElse(os.Getenv("SKIP_SMALL_FLUSH_THRESHOLD"), "1048576")),              // default 1MB: delay file creation until buffered bytes reach this threshold
		skipSmallFlush:                         os.Getenv("SKIP_SMALL_FLUSH") != "false",                                                           // enabled by default; when true, SD-card protection mode is enabled
		sequentialWrite:                        os.Getenv("SEQUENTIAL_WRITE") != "false",                                                           // enabled by default; set false to disable global flush serialization
		dropFilePageCache:                      os.Getenv("DROP_FILE_PAGE_CACHE") != "false",

		// danmaku recording performance configs (defaults tuned for Raspberry Pi 4B + SD card)
		danmakuWriterBufferSize:            utils.MustAtoi(utils.EmptyOrElse(os.Getenv("DANMAKU_WRITER_BUFFER_SIZE"), "262144")),             // 256KB: absorb bursts; fewer mid-interval flushes on SD
		danmakuWriterMinPeriodicFlushBytes: utils.MustAtoi(utils.EmptyOrElse(os.Getenv("DANMAKU_WRITER_MIN_PERIODIC_FLUSH_BYTES"), "16384")), // 16KB: skip tiny periodic flushes that contend with video
		danmakuWriterFlushPeriod:           utils.MustAtoi(utils.EmptyOrElse(os.Getenv("DANMAKU_WRITER_FLUSH_PERIOD_SECS"), "15")),           // align with video flush period to reduce SD card flush frequency
		danmakuWriterSyncPeriod:            utils.MustAtoi(utils.EmptyOrElse(os.Getenv("DANMAKU_WRITER_SYNC_PERIOD_SECS"), "0")),             // 0 = disabled; sync only on Close() to minimize SD card wear
		danmakuChanBufferSize:              utils.MustAtoi(utils.EmptyOrElse(os.Getenv("DANMAKU_CHAN_BUFFER_SIZE"), "256")),                  // message slots per room; overflow policy decides drop vs block
		danmakuBytesPoolSize:               utils.MustAtoi(utils.EmptyOrElse(os.Getenv("DANMAKU_BYTES_POOL_SIZE"), "4096")),                  // 4KB per fragment buffer
	}

	ReadOnly = &GlobalReadOnly{config: c}

	lc.Append(fx.StartHook(func(context.Context) error {
		if err := ReadOnly.Validate(); err != nil {
			return err
		}
		if err := os.MkdirAll(c.OutputDir, 0755); err != nil {
			return err
		}
		if err := os.MkdirAll(c.DatabaseDir, 0755); err != nil {
			return err
		}
		if err := os.MkdirAll(c.SecretDir, 0700); err != nil {
			return err
		}
		return nil
	}))

	return c, nil
}

func parseUsernameAndPassword(usernameKey, passwordKey string) (string, string, error) {
	username := os.Getenv(usernameKey)
	password := os.Getenv(passwordKey)
	if username == "" || password == "" {
		return "", "", nil
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", "", err
	}
	return username, string(passwordHash), nil
}

func parseDurationOrDefault(raw string, fallback time.Duration) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return d
}

func parseCommaSeparatedValues(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	vals := make([]string, 0, len(parts))
	for _, part := range parts {
		v := strings.TrimSpace(part)
		if v == "" {
			continue
		}
		vals = append(vals, v)
	}
	return vals
}

func resolveFRPToken(server, baseDomain string) string {
	token := os.Getenv("FRP_TOKEN")
	if token != "" {
		return token
	} else if server == officialFRPServer && baseDomain == officialFRPDomain {
		return frpTokenInjected
	} else {
		return ""
	}
}

var Module = fx.Module("config", fx.Provide(provider))
