//go:build cgo && android

package ffmpeg

/*
#cgo android,arm64 LDFLAGS: -L${SRCDIR}/../../lib/arm64-v8a -lffmpegkit
#cgo android,amd64 LDFLAGS: -L${SRCDIR}/../../lib/x86_64 -lffmpegkit
#include <stdlib.h>

// Use ffmpeg-kit native symbols directly so cancel can target one session.
extern int ffmpeg_execute(int argc, char** argv);
extern void addSession(long id);
extern void removeSession(long id);
extern void cancelSession(long id);
extern __thread long globalSessionId;

static int ffmpegkit_execute_session(long sessionId, int argc, char** argv) {
    globalSessionId = sessionId;
    addSession(sessionId);
    int rc = ffmpeg_execute(argc, argv);
    removeSession(sessionId);
    return rc;
}

static void ffmpegkit_cancel_session(long sessionId) {
    cancelSession(sessionId);
}
*/
import "C"
import (
	"context"
	"fmt"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/sirupsen/logrus"
)

var nextSessionID atomic.Int64

// Run 封裝了 ffmpeg.so 的執行邏輯，並完美支援 Context 取消與逾時机制
func Run(ctx context.Context, taskLog *logrus.Entry, args ...string) error {
	_ = taskLog

	if len(args) == 0 {
		return fmt.Errorf("arguments cannot be empty")
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	initFFmpegLoggerOnce()

	sessionID := C.long(nextSessionID.Add(1))

	finalArgs := args
	if args[0] != "ffmpeg" {
		finalArgs = append([]string{"ffmpeg"}, args...)
	}

	// 1. 將 Go 的 []string 轉換為 C 的 char** (argv)
	argc := C.int(len(finalArgs))
	cArgs := make([]*C.char, len(finalArgs))
	for i, arg := range finalArgs {
		cArgs[i] = C.CString(arg)
	}

	// 確保函式結束時，不論成功或失敗都會釋放 C 記憶體，避免手機 OOM 閃退
	defer func() {
		for _, cStr := range cArgs {
			C.free(unsafe.Pointer(cStr))
		}
	}()

	// 2. 建立一個帶緩衝的通道，用來接收 C 函式的結束狀態碼
	done := make(chan int, 1)

	// 3. 將會卡死的 C 函式丟進背景的 Goroutine 執行
	go func() {
		// 【Android 專屬優化：I/O 與 CPU 級別防禦】
		// 1. 強制將當前 Goroutine 鎖定在這個作業系統執行緒 (OS Thread) 上
		// 這樣可以確保後續的系統調用精準作用於當前執行 C 函式的執行緒
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		// 2. 獲取當前執行緒在 Linux 核心中的真實 TID (Thread ID)
		tid := syscall.Gettid()

		// 3. 將此執行緒的 Nice 值設為 19（最低優先級）
		// 在 Android 系統中，這會觸發核心將該執行緒丟進 background cgroup
		_ = syscall.Setpriority(syscall.PRIO_PROCESS, tid, 19)

		taskLog.Debugf("Started ffmpeg session %d with TID %d at lowest priority", sessionID, tid)

		// 這個 C 呼叫會一直阻塞，直到轉檔結束
		exitCode := C.ffmpegkit_execute_session(sessionID, argc, (**C.char)(unsafe.Pointer(&cArgs[0])))
		done <- int(exitCode)
	}()

	// 4. 監聽 Context 與 執行狀態
	select {
	case <-ctx.Done():
		// 💥 觸發情境：Dart 端取消了任務，或是 Context Timeout 時間到了
		taskLog.Warnf("FFmpeg 任务 %d 被取消或超时，正在强制停止 FFmpeg Session...", sessionID)

		// 只取消當前任務綁定的 session，避免影響其他並發任務
		C.ffmpegkit_cancel_session(sessionID)

		// 阻塞等待背景的 Goroutine 徹底退出，確保記憶體安全釋放
		<-done

		taskLog.Infof("FFmpeg 任务 %d 已经成功取消", sessionID)

		// 回傳標準 Go 脈絡錯誤：context.Canceled 或 context.DeadlineExceeded
		return ctx.Err()

	case exitCode := <-done:
		// 🎉 觸發情境：FFmpeg 正常執行完畢
		if exitCode != 0 {
			taskLog.Errorf("FFmpeg 任务 %d 执行失败，Exit Code: %d", sessionID, exitCode)
			return fmt.Errorf("ffmpeg execution failed with exit code: %d", exitCode)
		}
		taskLog.Infof("FFmpeg 任务 %d 已经成功执行完毕", sessionID)
		return nil
	}
}

// Available reports whether ffmpeg backend is usable in cgo builds.
// 在 cgo 模式下，ffmpeg 的可用性取決於是否能成功載入 ffmpegkit 的共享庫。
func Available() bool {
	return true
}
