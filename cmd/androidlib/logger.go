package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/sirupsen/logrus"
)

var (
	bootstrapLog *os.File
	logMu        sync.Mutex // 保護 bootstrapLog 指標的鎖
)

func safeLogF(format string, args ...interface{}) {
	safeGetLog(func(log *os.File) {
		fmt.Fprintf(log, format+"\n", args...)
	})
}

func safeLog(message string) {
	safeGetLog(func(log *os.File) {
		_, _ = log.WriteString(message + "\n")
	})
}

func safeGetLog(ifExist func(*os.File)) {
	logMu.Lock()
	if bootstrapLog == nil && basePath != nil {
		initBootstrapLog(*basePath)
	}

	targetLog := bootstrapLog

	logMu.Unlock()

	if targetLog != nil {
		ifExist(targetLog)
	}
}

func initBootstrapLog(basePath string) {
	path := filepath.Join(basePath, "bootstrap.log")
	log, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		_, _ = os.Stderr.WriteString("--- [SO ERROR] Failed to open bootstrap log: " + err.Error() + "\n")
		logrus.SetOutput(os.Stderr)
		return
	}

	logrus.SetOutput(log)
	bootstrapLog = log

	logrus.SetFormatter(&logrus.TextFormatter{
		TimestampFormat: "2006-01-02 15:04:05",
		FullTimestamp:   true,
		ForceColors:     false, // disable colors for file output
	})
}
