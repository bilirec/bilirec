package root

import (
	_ "embed"
	"os"
	"path/filepath"

	"github.com/bilirec/bilirec/pkg/logger"

	"github.com/joho/godotenv"
)

var log = logger.Named("root")

//go:embed .env
var dotEnvFile string

func InitDotEnv() {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		log.Debug("在 Docker 中运行，跳过 .env 加载")
		return
	}

	if err := godotenv.Load(".env.local"); err == nil {
		log.Info("已从 .env.local 文件加载环境变量")
		return
	} else if !os.IsNotExist(err) {
		log.Warnf("加载 .env.local 失败：%v, 将使用默认值", err)
	}

	// 如果 .env.local 不存在，则继续加载 .env

	exe, err := os.Executable()
	if err != nil {
		log.Warnf("无法确定可执行文件路径：%v", err)
		// fallback to cwd
		exe = "."
	}

	dotEnvPath := filepath.Join(filepath.Dir(exe), ".env")

	// 生成的 .env 文件会放在可执行文件同目录下，方便用户修改
	if _, err := os.Stat(dotEnvPath); os.IsNotExist(err) {
		if err := os.WriteFile(dotEnvPath, []byte(dotEnvFile), 0644); err != nil {
			log.Warnf("写入 .env 文件失败：%v，如果你使用的是二进制版本，请手动创建 .env 文件", err)
		} else {
			log.Info("已使用默认值生成 .env 文件；如果你使用的是二进制版本，每次修改 .env 后请重启")
		}
	}

	if err := godotenv.Load(dotEnvPath); err != nil {
		log.Debugf(".env 未加载：%v", err)
		return
	}

	log.Info("已从 .env 文件加载环境变量")
}
