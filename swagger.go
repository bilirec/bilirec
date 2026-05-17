package main

import (
	_ "embed"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

//go:embed docs/swagger.json
var embeddedSwagger []byte

var logger = logrus.WithField("package", "main")

func init() {
	exe, err := os.Executable()
	if err != nil {
		logger.Warnf("无法确定可执行文件路径：%v", err)
		// fallback to cwd
		exe = "."
	}
	exeDir := filepath.Dir(exe)
	swagDir := filepath.Join(exeDir, "docs")
	swagPath := filepath.Join(swagDir, "swagger.json")

	if _, err := os.Stat(swagPath); os.IsNotExist(err) {
		if err := os.MkdirAll(swagDir, 0755); err != nil {
			logger.Warnf("创建 docs 目录失败：%v", err)
		}
		if err := os.WriteFile(swagPath, embeddedSwagger, 0644); err != nil {
			logger.Warnf("写入内置 swagger 失败：%v", err)
		}
		logger.Infof("已将内置 swagger 写入 %s", swagPath)
	}
}
