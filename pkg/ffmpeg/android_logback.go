//go:build cgo && android

package ffmpeg

/*
#cgo android,arm64 LDFLAGS: -L${SRCDIR}/../../lib/arm64-v8a -lffmpegkit
#cgo android,amd64 LDFLAGS: -L${SRCDIR}/../../lib/x86_64 -lffmpegkit
#include <stdio.h>
#include <stdarg.h>
#include <string.h>

void av_log_set_callback(void (*callback)(void*, int, const char*, va_list));
void av_log_set_level(int level);

// 使用固定大小的缓冲区避免动态内存分配
extern void go_log_callback_bridge(int level, char* message, int len);

static void c_log_callback(void* ptr, int level, const char* fmt, va_list vl) {
    char buffer[2048];
    int len = vsnprintf(buffer, sizeof(buffer), fmt, vl);
    if (len > 0 && len < sizeof(buffer)) {
        // 传递长度，避免 Go 侧调用 strlen
        go_log_callback_bridge(level, buffer, len);
    }
}

static void register_ffmpeg_log_callback() {
    av_log_set_callback(c_log_callback);
}
*/
import "C"
import (
	"strings"
	"sync"
	"unsafe"

	"github.com/sirupsen/logrus"
)

var (
	coreLog    = logrus.WithField("component", "ffmpeg_core")
	logQueue   = make(chan logMessage, 256) // 缓冲通道
	loggerOnce sync.Once
)

type logMessage struct {
	level   int
	message string
}

// initFFmpegLoggerOnce 显式初始化（在确保 FFmpeg 库已加载后调用）
func initFFmpegLoggerOnce() {
	loggerOnce.Do(func() {
		go processLogMessages()

		defer func() {
			if r := recover(); r != nil {
				coreLog.Errorf("注冊 ffmpeg logging hook 失敗 : %v", r)
			}
		}()

		C.register_ffmpeg_log_callback()
		C.av_log_set_level(32) // AV_LOG_INFO
		coreLog.Info("ffmpeg logging hook 注冊成功")
	})
}

//export go_log_callback_bridge
func go_log_callback_bridge(level C.int, message *C.char, length C.int) {

	if message == nil || length <= 0 {
		return
	}

	// 使用固定长度避免调用 C.GoString（它内部会调用 strlen）
	buf := C.GoBytes(unsafe.Pointer(message), length)
	goStr := string(buf)

	// 非阻塞发送到通道，避免死锁
	select {
	case logQueue <- logMessage{level: int(level), message: goStr}:
	default:
		// 如果通道满了，丢弃这条日志（总比崩溃好）
	}
}

// 在独立的 Go goroutine 中处理日志（安全）
func processLogMessages() {
	for msg := range logQueue {
		goStr := strings.TrimRight(msg.message, "\r\n")
		if goStr == "" {
			continue
		}

		switch msg.level {
		case 8: // AV_LOG_FATAL
			coreLog.Errorf("[FFmpeg FATAL] %s", goStr)
		case 16: // AV_LOG_ERROR
			coreLog.Errorf("[FFmpeg ERROR] %s", goStr)
		case 24: // AV_LOG_WARNING
			coreLog.Warnf("[FFmpeg WARN]  %s", goStr)
		case 32: // AV_LOG_INFO
			coreLog.Infof("[FFmpeg INFO]  %s", goStr)
		case 40: // AV_LOG_VERBOSE
			coreLog.Infof("[FFmpeg VERB]  %s", goStr)
		default: // AV_LOG_DEBUG
			coreLog.Debugf("[FFmpeg DEBUG] %s", goStr)
		}
	}
}
