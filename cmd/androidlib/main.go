//go:build cgo

package main

/*
#include <stdint.h>
*/
import "C"

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/eric2788/bilirec/internal/bootstrap"
	"go.uber.org/fx"
)

var (
	androidApp *fx.App
	appMu      sync.Mutex
)

//export Start
func Start() C.int {
	appMu.Lock()
	defer appMu.Unlock()

	if androidApp != nil {
		return 0
	}

	if os.Getenv("HOST") == "" {
		_ = os.Setenv("HOST", "127.0.0.1")
	}
	if os.Getenv("PORT") == "" {
		_ = os.Setenv("PORT", "8080")
	}

	_ = os.Setenv("BILIBILI_LOGIN_MODE", "controller") // avoid process stucked on foreground service
	_ = os.Setenv("SKIP_SMALL_FLUSH", "true")          // enable sdcard protection
	_ = os.Setenv("FRP_ENABLED", "false")              // not expected to expose to public network in android

	app := bootstrap.NewApp()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if err := app.Start(ctx); err != nil {
		return 1
	}

	androidApp = app
	return 0
}

//export Stop
func Stop() C.int {
	appMu.Lock()
	defer appMu.Unlock()

	if androidApp == nil {
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if err := androidApp.Stop(ctx); err != nil {
		return 1
	}

	androidApp = nil
	return 0
}

func main() {}
