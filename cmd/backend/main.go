package main

import (
	root "github.com/bilirec/bilirec"
	"github.com/bilirec/bilirec/internal/bootstrap"
	"github.com/sirupsen/logrus"
)

func main() {
	root.InitDotEnv()
	root.InitSwaggerDocs()

	logrus.Info("Starting bilirec backend...")
	bootstrap.NewApp().Run()
}
