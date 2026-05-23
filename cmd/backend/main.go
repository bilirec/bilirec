package main

import (
	root "github.com/eric2788/bilirec"
	"github.com/eric2788/bilirec/internal/bootstrap"
	"github.com/sirupsen/logrus"
)

func main() {
	root.InitDotEnv()
	root.InitSwaggerDocs()

	logrus.Info("Starting bilirec backend...")
	bootstrap.NewApp().Run()
}
