package config

import (
	"net/url"
	"os"

	"github.com/eric2788/bilirec/utils"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"golang.org/x/crypto/bcrypt"
)

// all config will be loaded from environment variables
type Config struct {
	AnonymousLogin bool
	Port           string

	MaxConcurrentRecordings int
	MaxRecordingHours       int
	MaxRecoveryAttempts     int
	MaxRetryMinutes         int

	OutputDir   string
	SecretDir   string
	DatabaseDir string

	ConvertFLVToMp4       bool
	DeleteFlvAfterConvert bool
	CloudConvertThreshold int64
	CloudConvertApiKey    string

	BackendHost        string
	FrontendURL        *url.URL
	Username           string
	PasswordHash       string
	ViewerUsername     string
	ViewerPasswordHash string
	JwtSecret          string

	Debug          bool
	ProductionMode bool

	MinDiskSpaceBytes int64

	CloudConvertCheckIntervalSecs      int
	CloudConvertMaxConcurrentDownloads int
	FFmpegCheckIntervalSecs            int
	FFmpegMaxConcurrentTasks           int
	FFmpegAllowDuringRecording         bool

	// configurable global performances
	uploadBufferSize           int
	downloadBufferSize         int
	streamWriterBufferSize     int
	liveStreamWriterBufferSize int
}

var logger = logrus.WithField("module", "config")

func provider() (*Config, error) {

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
		logrus.SetLevel(logrus.DebugLevel)
	}

	c := &Config{
		AnonymousLogin:                     os.Getenv("ANONYMOUS_LOGIN") == "true",
		Port:                               utils.EmptyOrElse(os.Getenv("PORT"), "8080"),
		MaxConcurrentRecordings:            utils.MustAtoi(utils.EmptyOrElse(os.Getenv("MAX_CONCURRENT_RECORDINGS"), "3")),
		MaxRecordingHours:                  utils.MustAtoi(utils.EmptyOrElse(os.Getenv("MAX_RECORDING_HOURS"), "5")),
		MaxRecoveryAttempts:                utils.MustAtoi(utils.EmptyOrElse(os.Getenv("MAX_RECOVERY_ATTEMPTS"), "5")),
		MaxRetryMinutes:                    utils.MustAtoi(utils.EmptyOrElse(os.Getenv("MAX_RETRY_MINUTES"), "10")),
		OutputDir:                          utils.EmptyOrElse(os.Getenv("OUTPUT_DIR"), "records"),
		SecretDir:                          utils.EmptyOrElse(os.Getenv("SECRET_DIR"), "secrets"),
		DatabaseDir:                        utils.EmptyOrElse(os.Getenv("DATABASE_DIR"), "database"),
		CloudConvertThreshold:              utils.MustAtoi64(utils.EmptyOrElse(os.Getenv("CLOUDCONVERT_THRESHOLD"), "1073741824")), // 1 GB
		CloudConvertApiKey:                 os.Getenv("CLOUDCONVERT_API_KEY"),                                                      // empty to disable
		ConvertFLVToMp4:                    os.Getenv("CONVERT_FLV_TO_MP4") == "true",
		DeleteFlvAfterConvert:              os.Getenv("DELETE_FLV_AFTER_CONVERT") == "true",
		FrontendURL:                        url,
		BackendHost:                        utils.EmptyOrElse(os.Getenv("BACKEND_HOST"), "localhost:8080"),
		Username:                           username,
		PasswordHash:                       string(passwordHash),
		ViewerUsername:                     viewerUsername,
		ViewerPasswordHash:                 string(viewerPasswordHash),
		JwtSecret:                          utils.EmptyOrElse(os.Getenv("JWT_SECRET"), "bilirec_secret"),
		Debug:                              debug,
		ProductionMode:                     os.Getenv("PRODUCTION_MODE") == "true",
		MinDiskSpaceBytes:                  utils.MustAtoi64(utils.EmptyOrElse(os.Getenv("MIN_DISK_SPACE_BYTES"), "5368709120")), // 5GB
		CloudConvertCheckIntervalSecs:      utils.MustAtoi(utils.EmptyOrElse(os.Getenv("CLOUDCONVERT_CHECK_INTERVAL_SECS"), "180")),
		CloudConvertMaxConcurrentDownloads: utils.MustAtoi(utils.EmptyOrElse(os.Getenv("CLOUDCONVERT_MAX_CONCURRENT_DOWNLOADS"), "1")),
		FFmpegCheckIntervalSecs:            utils.MustAtoi(utils.EmptyOrElse(os.Getenv("FFMPEG_CHECK_INTERVAL_SECS"), "60")),
		FFmpegMaxConcurrentTasks:           utils.MustAtoi(utils.EmptyOrElse(os.Getenv("FFMPEG_MAX_CONCURRENT_TASKS"), "1")),
		FFmpegAllowDuringRecording:         os.Getenv("FFMPEG_ALLOW_DURING_RECORDING") == "true",

		// global performance configs
		uploadBufferSize:           utils.MustAtoi(utils.EmptyOrElse(os.Getenv("UPLOAD_BUFFER_SIZE"), "5242880")),             // default 5MB
		downloadBufferSize:         utils.MustAtoi(utils.EmptyOrElse(os.Getenv("DOWNLOAD_BUFFER_SIZE"), "5242880")),           // default 5MB
		streamWriterBufferSize:     utils.MustAtoi(utils.EmptyOrElse(os.Getenv("STREAM_WRITER_BUFFER_SIZE"), "1048576")),      // default 1MB
		liveStreamWriterBufferSize: utils.MustAtoi(utils.EmptyOrElse(os.Getenv("LIVE_STREAM_WRITER_BUFFER_SIZE"), "5242880")), // 5MB: optimal for 1080p30fps (4.5Mbps = 2.81MB/5s)
	}

	ReadOnly = &GlobalReadOnly{config: c}
	ReadOnly.Validate()
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

var Module = fx.Module("config", fx.Provide(provider))
