//go:build cgo

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
)

var (
	androidApp *fx.App
	appMu      sync.Mutex

	basePath *string // 用於記錄啟動和停止事件的日誌文件位置
)

type StartConfig struct {
	BasePath    string            `json:"basePath"` // only this is required, others have defaults
	Port        int               `json:"port"`
	Host        string            `json:"host"`
	FrontendURL string            `json:"frontendUrl"`
	OutputDir   string            `json:"outputDir"`
	Username    string            `json:"username"`
	Password    string            `json:"password"`
	SSEToken    string            `json:"sseToken"`
	Env         map[string]string `json:"env"` // for future extensibility
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
		safeLog("-------------- STOP FAILED at " + time.Now().Format("2006-01-02 15:04:05") + " ---------------")
	} else {
		safeLog("-------------- STOP at " + time.Now().Format("2006-01-02 15:04:05") + " ---------------")
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

	basePath = &config.BasePath

	initBootstrapLog(config.BasePath)
	loadAndroidTimeZone()

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

	_ = os.Setenv("LIVE_STREAM_WRITER_BUFFER_SIZE", "4194304") // 4MB bufio writer buffer, balance between memory usage and write performance
	_ = os.Setenv("LIVE_STREAM_WRITER_CHAN_BUFFER_SIZE", "32") // 512KB * 32 = 16MB buffer per stream, balance between memory usage and smoothness
	_ = os.Setenv("LIVE_STREAM_WRITER_SYNC_PERIOD_SECS", "0")
	_ = os.Setenv("LIVE_STREAM_WRITER_FLUSH_PERIOD_SECS", "10")

	_ = os.Setenv("SKIP_SMALL_FLUSH", "false")
	_ = os.Setenv("SILENT_ACCESS_LOG", "true")
	_ = os.Setenv("CONVERT_TO_MP4", "false")

	// override!
	for key, value := range config.Env {
		_ = os.Setenv(key, value)
	}

	loadEnvironmentTimeZone()
	initResourceLimits()

	app := bootstrap.NewAndroidApp()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	safeLog("-------------- START at " + time.Now().Format("2006-01-02 15:04:05") + " --------------")

	if err := app.Start(ctx); err != nil {
		return 1
	}

	androidApp = app
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

	safeLogF("System limits dynamically adjusted: Max Recordings=%d, Mem Limit=%d MB, CPU Cores=%d/%d\n",
		maxConcurrent, targetMemoryMB, finalCPUs, totalCPUs)
}

func main() {}
