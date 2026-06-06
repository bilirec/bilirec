//go:build !cgo || !android

package ffmpeg

import (
	"context"
	"os/exec"
	"slices"

	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/sirupsen/logrus"
)

// 初始容量 (initialCap): 4KB (剛好是一個記憶體分頁，應付絕大多數錯誤綽綽有餘)
// 最大容量 (maxCap): 4MB (防止極端 Warn 洪水撐爆池子)
var ffmpegBufPool = pool.NewBufferPool(4*1024, 4*1024*1024)

func Run(ctx context.Context, taskLog *logrus.Entry, args ...string) error {

	// 如果開啟了 Debug，依然走「直通水管」即時看完整日誌
	if taskLog.Logger.IsLevelEnabled(logrus.DebugLevel) {
		cmd := exec.CommandContext(ctx, "ffmpeg", args...)

		debugWriter := taskLog.WriterLevel(logrus.DebugLevel)
		defer debugWriter.Close()

		cmd.Stdout = debugWriter
		cmd.Stderr = debugWriter
		return cmd.Run()
	}

	buf := ffmpegBufPool.Get()
	defer ffmpegBufPool.Put(buf)

	argsWithErrLogLevel := args
	if !slices.Contains(args, "-loglevel") {
		argsWithErrLogLevel = append([]string{"-loglevel", "error"}, args...)
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", argsWithErrLogLevel...)
	cmd.Stdout = nil
	cmd.Stderr = buf
	err := cmd.Run()
	if err != nil {
		taskLog.WithError(err).Errorf("FFmpeg 执行失败，底层日志如下:\n%s", buf.String())
	}
	return err
}

// Available reports whether external ffmpeg command is available in PATH.
func Available() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}
