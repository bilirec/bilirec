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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eric2788/bilirec/internal/bootstrap"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

var (
	androidApp *fx.App
	appMu      sync.Mutex
)

type StartConfig struct {
	BasePath    string `json:"basePath"`
	Port        int    `json:"port"`
	Host        string `json:"host"`
	FrontendURL string `json:"frontendUrl"`
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

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if err := androidApp.Stop(ctx); err != nil {
		return 1
	}

	androidApp = nil
	return 0
}

// private functions

func start(config StartConfig) C.int {
	appMu.Lock()
	defer appMu.Unlock()

	if androidApp != nil {
		return 0
	}

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

	_ = os.Setenv("OUTPUT_DIR", filepath.Join(config.BasePath, "records"))
	_ = os.Setenv("SECRET_DIR", filepath.Join(config.BasePath, "secrets"))
	_ = os.Setenv("DATABASE_DIR", filepath.Join(config.BasePath, "database"))
	_ = os.Setenv("HOST", config.Host)
	_ = os.Setenv("PORT", strconv.Itoa(config.Port))
	_ = os.Setenv("FRONTEND_URL", config.FrontendURL)

	_ = os.Setenv("BILIBILI_LOGIN_MODE", "controller") // avoid process stucked on foreground service
	_ = os.Setenv("SKIP_SMALL_FLUSH", "true")          // enable sdcard protection
	_ = os.Setenv("FRP_ENABLED", "false")              // not expected to expose to public network in android

	// Setup file logging for Android
	if err := setupFileLogging(config.BasePath); err != nil {
		return 1
	}

	app := bootstrap.NewApp()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if err := app.Start(ctx); err != nil {
		return 1
	}

	androidApp = app
	return 0
}

func setupFileLogging(basePath string) error {
	logDir := filepath.Join(basePath, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}

	logFile := filepath.Join(logDir, "bilirec.log")
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	// Set logrus to write to file with timestamp and level
	logrus.SetFormatter(&logrus.TextFormatter{
		TimestampFormat: "2006-01-02 15:04:05",
		FullTimestamp:   true,
		ForceColors:     false, // disable colors for file output
	})
	logrus.SetOutput(file)

	return nil
}

func main() {}
