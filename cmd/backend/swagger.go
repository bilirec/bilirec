package main

import (
	"os"
	"path/filepath"

	apidocs "github.com/eric2788/bilirec/docs"
)

func init() {
	exe, err := os.Executable()
	if err != nil {
		logger.Warnf("无法确定可执行文件路径：%v", err)
		exe = "."
	}

	exeDir := filepath.Dir(exe)
	swagDir := filepath.Join(exeDir, "docs")
	swagPath := filepath.Join(swagDir, "swagger.json")

	if _, err := os.Stat(swagPath); err == nil {
		return
	}

	if err := os.MkdirAll(swagDir, 0755); err != nil {
		logger.Warnf("创建 docs 目录失败：%v", err)
		return
	}

	swaggerJSON := apidocs.SwaggerInfo.ReadDoc()
	if swaggerJSON == "" {
		logger.Warn("swagger 文档模板为空，跳过 swagger.json 生成")
		return
	}

	if err := os.WriteFile(swagPath, []byte(swaggerJSON), 0644); err != nil {
		logger.Warnf("写入内置 swagger 失败：%v", err)
		return
	}

	logger.Infof("已将内置 swagger 写入 %s", swagPath)
}
