package main

import (
	"os"

	"github.com/joho/godotenv"
)

func init() {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		logger.Debug("在 Docker 中运行，跳过 .env 加载")
		return
	}

	if err := godotenv.Load(); err != nil {
		logger.Debugf(".env 未加载：%v", err)
		return
	}

	logger.Info("已从 .env 文件加载环境变量")
}
