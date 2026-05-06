package main_test

import (
	"os"
	"testing"
	"time"

	main "github.com/eric2788/bilirec"
	"go.uber.org/fx/fxtest"
)

func TestAppLaunch(t *testing.T) {
	app := fxtest.New(t, main.MainModule())
	app.RequireStart()
	defer app.RequireStop()
	<-time.After(10 * time.Second)
	t.Log("REST app started successfully")
}

func init() {
	os.Setenv("BILIBILI_ANONYMOUS_LOGIN", "true")
}
