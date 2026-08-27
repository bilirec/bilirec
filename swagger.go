package root

import (
	"os"
	"path/filepath"

	_ "embed"
)

//go:embed docs/swagger.json
var embeddedSwagger []byte

func InitSwaggerDocs() {
	exe, err := os.Executable()
	if err != nil {
		log.Warnf("无法确定可执行文件路径：%v", err)
		// fallback to cwd
		exe = "."
	}

	swagDir := filepath.Join(filepath.Dir(exe), "docs")
	swagPath := filepath.Join(swagDir, "swagger.json")

	if _, err := os.Stat(swagPath); os.IsNotExist(err) {
		if err := os.MkdirAll(swagDir, 0755); err != nil {
			log.Warnf("创建 docs 目录失败：%v", err)
		}
		if err := os.WriteFile(swagPath, embeddedSwagger, 0644); err != nil {
			log.Warnf("写入内置 swagger 失败：%v", err)
		}
		log.Infof("已将内置 swagger 写入 %s", swagPath)
	}
}
