//go:build cgo && android

package main

/*
#include <stdint.h>
*/
import "C"

import (
	"context"
	"encoding/json"
	"math"
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
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	androidApp *fx.App
	appMu      sync.Mutex
	logger     *lumberjack.Logger
)

type StartConfig struct {
	BasePath string            `json:"basePath"`
	Env      map[string]string `json:"env"`
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
		logrus.Info("-------------- STOP FAILED at " + time.Now().Format("2006-01-02 15:04:05") + " ---------------")
	} else {
		logrus.Info("-------------- STOP at " + time.Now().Format("2006-01-02 15:04:05") + " ---------------")
	}

	closeBootstrapLog(logger)
	androidApp = nil
	logger = nil
	return C.int(exitCode)
}

// private functions

func start(config StartConfig) C.int {
	appMu.Lock()
	defer appMu.Unlock()

	if androidApp != nil {
		return 0
	}

	log := initBootstrapLog(config.BasePath)
	loadAndroidTimeZone()

	for key, value := range defaultAndroidEnv(config.BasePath) {
		_ = os.Setenv(key, value)
	}

	username, hasUsername := config.Env["USERNAME"]
	password, hasPassword := config.Env["PASSWORD"]

	for key, value := range config.Env {
		if value == "" {
			continue
		}

		if key == "USERNAME" || key == "PASSWORD" {
			continue
		}

		_ = os.Setenv(key, value)
	}

	if hasUsername && hasPassword && username != "" && password != "" {
		_ = os.Setenv("USERNAME", username)
		_ = os.Setenv("PASSWORD", password)
	}

	loadEnvironmentTimeZone()
	initResourceLimits()

	app := bootstrap.NewAndroidApp()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	logrus.Info("-------------- START at " + time.Now().Format("2006-01-02 15:04:05") + " --------------")

	if err := app.Start(ctx); err != nil {
		closeBootstrapLog(log)
		return 1
	}

	androidApp = app
	logger = log
	return 0
}

func initResourceLimits() {
	// 1. 獲取用戶設定的最大錄製數量 (預設為 3)
	maxConcurrentStr := os.Getenv("MAX_CONCURRENT_RECORDINGS")
	maxConcurrent, err := strconv.Atoi(maxConcurrentStr)
	if err != nil || maxConcurrent < 1 {
		maxConcurrent = 3
	}

	// 2. 動態計算記憶體天花板 (Soft Memory Limit)
	// 公式: 基礎開銷(60MB) + (最大錄製數 * 單路開銷(36MB))
	const baseMemoryMB = 60
	const perStreamMemoryMB = 36

	targetMemoryMB := baseMemoryMB + (maxConcurrent * perStreamMemoryMB)
	targetMemoryBytes := int64(targetMemoryMB * 1024 * 1024)

	// 設定記憶體上限
	debug.SetMemoryLimit(targetMemoryBytes)

	// 3. 動態配置 CPU 核心數 (GOMAXPROCS)
	// 公式: 預設 2 核，每多 3 路錄製增加 1 核，但上限不超過設備物理核心數的一半
	totalCPUs := runtime.NumCPU()
	calculatedCPUs := 2 + (maxConcurrent / 3)

	maxAllowedCPUs := int(math.Max(2, float64(totalCPUs)/2.0)) // 至少2核，至多一半核心

	finalCPUs := max(min(calculatedCPUs, maxAllowedCPUs, totalCPUs), 1) // 最終值：同時不超過 calculated / maxAllowed / total + 防呆，避免任何極端情況變成 0

	runtime.GOMAXPROCS(finalCPUs)

	logrus.Infof("System limits dynamically adjusted: Max Recordings=%d, Mem Limit=%d MB, CPU Cores=%d/%d",
		maxConcurrent, targetMemoryMB, finalCPUs, totalCPUs)
}

func defaultAndroidEnv(basePath string) map[string]string {
	return map[string]string{
		"HOST":                                       "127.0.0.1",
		"PORT":                                       "8080",
		"FRONTEND_URL":                               "https://app.bilirec.org",
		"OUTPUT_DIR":                                 filepath.Join(basePath, "records"),
		"SECRET_DIR":                                 filepath.Join(basePath, "secrets"),
		"DATABASE_DIR":                               filepath.Join(basePath, "database"),
		"BILIBILI_LOGIN_MODE":                        "controller",
		"FRP_ENABLED":                                "false",
		"MIN_DISK_SPACE_BYTES":                       "2147483648", // 2GB
		"LIVE_STREAM_WRITER_BUFFER_SIZE":             "4194304",    // 8MB -> 4MB
		"LIVE_STREAM_WRITER_CHAN_BUFFER_SIZE":        "32",         // 64 -> 32
		"READ_STREAM_BYTES_POOL_SIZE_HIGH":           "1048576",    // 1MB
		"READ_STREAM_CHAN_BUFFER_SIZE_HIGH":          "32",         // 48 -> 32
		"LIVE_STREAM_WRITER_BYTES_POOL_SIZE_HIGH":    "1048576",    // 1MB
		"LIVE_STREAM_WRITER_SYNC_PERIOD_SECS":        "0",
		"LIVE_STREAM_WRITER_COLD_CACHE_RELEASE_SECS": "60",
		"LIVE_STREAM_WRITER_FLUSH_PERIOD_SECS":       "15",
		"SKIP_SMALL_FLUSH":                           "false",
		"DROP_FILE_PAGE_CACHE":                       "true",
		"SILENT_ACCESS_LOG":                          "true",
		"SEQUENTIAL_WRITE":                           "false",
	}
}

func main() {}
