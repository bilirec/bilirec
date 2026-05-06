package main

import (
	"github.com/eric2788/bilirec/internal/bootstrap"
	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("package", "backend")

func main() {
	bootstrap.NewApp().Run()
}
