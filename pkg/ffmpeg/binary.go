//go:build !cgo || !android

package ffmpeg

import (
	"context"
	"os/exec"

	"github.com/sirupsen/logrus"
)

func Run(ctx context.Context, taskLog *logrus.Entry, args ...string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	debugWriter := taskLog.WriterLevel(logrus.DebugLevel)
	defer debugWriter.Close()
	cmd.Stdout = debugWriter
	cmd.Stderr = debugWriter
	return cmd.Run()
}

// Available reports whether external ffmpeg command is available in PATH.
func Available() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}
