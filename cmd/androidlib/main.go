//go:build cgo

package main

/*
#include <stdint.h>
*/
import "C"

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bilirec/bilirec/internal/bootstrap"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

var (
	androidApp *fx.App
	appMu      sync.Mutex

	bootstrapLog *os.File
	logMu        sync.Mutex // 保護 bootstrapLog 指標的鎖

	basePath *string // 用於記錄啟動和停止事件的日誌文件位置
)

type StartConfig struct {
	BasePath    string `json:"basePath"` // only this is required, others have defaults
	Port        int    `json:"port"`
	Host        string `json:"host"`
	FrontendURL string `json:"frontendUrl"`
	OutputDir   string `json:"outputDir"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	SSEToken    string `json:"sseToken"`
}

//export Start
func Start(configJson *C.char) C.int {
	if configJson == nil {
		return 1
	}

	jsonStr := strings.TrimSpace(C.GoString(configJson))
	if jsonStr == "" {
		return 1
	}

	var config StartConfig
	if err := json.Unmarshal([]byte(jsonStr), &config); err != nil {
		return 1
	}

	if config.BasePath == "" {
		return 1
	}

	return start(config)
}

//export Stop
func Stop() C.int {
	appMu.Lock()
	defer appMu.Unlock()

	if androidApp == nil {
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	exitCode := 0
	if err := androidApp.Stop(ctx); err != nil {
		logrus.Errorf("failed to stop app: %v\n", err)
		exitCode = 1
		getBootstrapLog(func(log *os.File) {
			_, _ = log.WriteString("-------------- STOP FAILED at " + time.Now().Format("2006-01-02 15:04:05") + " ---------------\n")
		})
	} else {
		getBootstrapLog(func(log *os.File) {
			_, _ = log.WriteString("-------------- STOP at " + time.Now().Format("2006-01-02 15:04:05") + " ---------------\n")
		})
	}

	androidApp = nil
	return C.int(exitCode)
}

// private functions

func start(config StartConfig) C.int {
	appMu.Lock()
	defer appMu.Unlock()

	if androidApp != nil {
		return 0
	}

	runtime.GOMAXPROCS(2)                   // 限制 2 個核心，防止手機過熱降頻
	debug.SetMemoryLimit(180 * 1024 * 1024) // 軟性記憶體天花板 180MiB (確保 3 路穩定)

	// Set default values if not provided
	if config.Port == 0 {
		config.Port = 8080
	}
	if config.Host == "" {
		config.Host = "127.0.0.1"
	}
	if config.FrontendURL == "" {
		config.FrontendURL = "https://app.bilirec.org"
	}
	if config.OutputDir == "" {
		config.OutputDir = filepath.Join(config.BasePath, "records")
	}

	if config.Username != "" && config.Password != "" {
		_ = os.Setenv("USERNAME", config.Username)
		_ = os.Setenv("PASSWORD", config.Password)
	}

	_ = os.Setenv("SECRET_DIR", filepath.Join(config.BasePath, "secrets"))
	_ = os.Setenv("DATABASE_DIR", filepath.Join(config.BasePath, "database"))
	_ = os.Setenv("OUTPUT_DIR", config.OutputDir)
	_ = os.Setenv("HOST", config.Host)
	_ = os.Setenv("PORT", strconv.Itoa(config.Port))
	_ = os.Setenv("FRONTEND_URL", config.FrontendURL)
	_ = os.Setenv("NOTIFY_SSE_TOKEN", config.SSEToken) // for android local notifications

	_ = os.Setenv("BILIBILI_LOGIN_MODE", "controller") // avoid the process getting stuck in foreground service
	_ = os.Setenv("FRP_ENABLED", "false")              // not expected to expose to public network in android

	_ = os.Setenv("MIN_DISK_SPACE_BYTES", "2147483648") // 2GB

	_ = os.Setenv("STREAM_WRITER_BUFFER_SIZE", "262144")       // 256KB
	_ = os.Setenv("LIVE_STREAM_WRITER_BUFFER_SIZE", "1048576") // 1MB
	_ = os.Setenv("LIVE_STREAM_WRITER_CHAN_BUFFER_SIZE", "10")
	_ = os.Setenv("LIVE_STREAM_WRITER_SYNC_PERIOD_SECS", "45") // sync every 45 seconds, prevent data loss while keeping reasonable performance
	_ = os.Setenv("LIVE_STREAM_WRITER_FLUSH_PERIOD_SECS", "5")

	_ = os.Setenv("SKIP_SMALL_FLUSH", "false")
	_ = os.Setenv("SILENT_ACCESS_LOG", "true")
	_ = os.Setenv("CONVERT_TO_MP4", "false")

	// 顯式設定
	_ = os.Setenv("SEQUENTIAL_WRITE", "true")       // Android I/O scheduler 友好，防止並發寫入搶佔前台 UI
	_ = os.Setenv("MAX_CONCURRENT_RECORDINGS", "3") // 顯式鎖定，防止默認值將來靜默變更

	basePath = &config.BasePath

	app := bootstrap.NewAndroidApp()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	getBootstrapLog(func(log *os.File) {
		_, _ = log.WriteString("-------------- START at " + time.Now().Format("2006-01-02 15:04:05") + " --------------\n")
	})

	if err := app.Start(ctx); err != nil {
		return 1
	}

	androidApp = app
	return 0
}

func getBootstrapLog(ifExist func(*os.File)) {
	logMu.Lock()
	if bootstrapLog == nil && basePath != nil {

		log, err := os.OpenFile(filepath.Join(*basePath, "bootstrap.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			logrus.SetOutput(log)
			bootstrapLog = log
		} else {
			_, _ = os.Stderr.WriteString("--- [SO ERROR] Failed to open bootstrap log: " + err.Error() + "\n")
			logrus.SetOutput(os.Stderr)
		}

		logrus.SetFormatter(&logrus.TextFormatter{
			TimestampFormat: "2006-01-02 15:04:05",
			FullTimestamp:   true,
			ForceColors:     false, // disable colors for file output
		})
	}

	targetLog := bootstrapLog

	logMu.Unlock()

	if targetLog != nil {
		ifExist(targetLog)
	}
}

func main() {}
