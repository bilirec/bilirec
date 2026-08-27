package main

import (
	root "github.com/bilirec/bilirec"
	"github.com/bilirec/bilirec/internal/bootstrap"
	"github.com/bilirec/bilirec/pkg/logger"
)

func main() {
	logger.Init(logger.Options{})
	defer logger.Sync()

	root.InitDotEnv()
	root.InitSwaggerDocs()

	logger.L().Info("Starting bilirec backend...")
	bootstrap.NewApp().Run()
}
