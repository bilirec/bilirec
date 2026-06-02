package main

import (
	"io"
	"log"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"gopkg.in/natefinch/lumberjack.v2"
)

func initBootstrapLog(basePath string) *lumberjack.Logger {
	path := filepath.Join(basePath, "bootstrap.log")
	logOutput := &lumberjack.Logger{
		Filename: path,
		MaxSize:  5,     // 上限到 5MB
		MaxAge:   7,     // 只留 7 天
		Compress: false, // 不壓縮
	}

	log.SetOutput(logOutput)
	logrus.SetOutput(logOutput)

	logrus.SetFormatter(&logrus.TextFormatter{
		TimestampFormat: "2006-01-02 15:04:05",
		FullTimestamp:   true,
		ForceColors:     false, // disable colors for file output
	})

	return logOutput
}

func closeBootstrapLog(logger *lumberjack.Logger) {
	log.SetOutput(io.Discard)
	logrus.SetOutput(io.Discard)
	logger.Close()
}

func init() {
	log.SetOutput(io.Discard)
	logrus.SetOutput(io.Discard)
}
