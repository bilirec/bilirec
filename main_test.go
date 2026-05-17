package main_test

import (
	"os"
	"testing"
	"time"

	"github.com/eric2788/bilirec/internal/bootstrap"
	"go.uber.org/fx/fxtest"
)

func TestAppLaunch(t *testing.T) {
	app := fxtest.New(t, bootstrap.MainModule())
	app.RequireStart()
	defer app.RequireStop()
	<-time.After(10 * time.Second)
	t.Log("REST app started successfully")
}

func init() {
	os.Setenv("BILIBILI_LOGIN_MODE", "anonymous")
}
