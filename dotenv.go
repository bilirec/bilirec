package main

import (
	_ "embed"
	"os"

	"github.com/joho/godotenv"
)

//go:embed .env
var dotEnvFile string

func init() {

	if _, err := os.Stat("/.dockerenv"); err == nil {
		logger.Debug("running in Docker, skipping .env generation")
		return
	}

	// 生成的 .env 文件会放在可执行文件同目录下，方便用户修改
	if _, err := os.Stat(".env"); os.IsNotExist(err) {
		if err := os.WriteFile(".env", []byte(dotEnvFile), 0644); err != nil {
			logger.Warnf("写入 .env 文件失败：%v，如果你使用的是二进制版本，请手动创建 .env 文件", err)
		} else {
			logger.Info("已使用默认值生成 .env 文件；如果你使用的是二进制版本，每次修改 .env 后请重启")
		}
	}

	if err := godotenv.Load(); err != nil {
		logger.Warnf("加载 .env 文件失败：%v，如果你使用的是二进制版本请重启", err)
	} else {
		logger.Info("已从 .env 文件加载环境变量")
	}

}
