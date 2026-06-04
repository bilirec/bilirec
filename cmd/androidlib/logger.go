//go:build cgo && android

package main

/*
#include <jni.h>
#include <stdlib.h>

// 留在邊界層處理 JNI 指標轉換
static const char* get_jni_string(JNIEnv* env, jstring str) {
    return (*env)->GetStringUTFChars(env, str, NULL);
}

static void release_jni_string(JNIEnv* env, jstring str, const char* chars) {
    (*env)->ReleaseStringUTFChars(env, str, chars);
}
*/
import "C"
import (
	"io"
	"log"
	"path/filepath"

	"github.com/bilirec/bilirec/pkg/ffmpeg"
	"github.com/sirupsen/logrus"

	"gopkg.in/natefinch/lumberjack.v2"
)

//export Java_org_bilirec_bilirec_LogBridge_sendFFmpegLog
func Java_org_bilirec_bilirec_LogBridge_sendFFmpegLog(env *C.JNIEnv, clazz C.jclass, jSessionId C.jlong, jLevel C.jint, jMsg C.jstring) {
	if jMsg == 0 {
		return
	}

	// 1. 在邊界層提取 C 字串
	cMsg := C.get_jni_string(env, jMsg)
	if cMsg == nil {
		return
	}
	defer C.release_jni_string(env, jMsg, cMsg)

	// 2. 轉換為純 Go 類型
	goMsg := C.GoString(cMsg)

	ffmpeg.ConsumeNativeLog(int64(jSessionId), int(jLevel), goMsg)
}

func initBootstrapLog(basePath string) *lumberjack.Logger {
	path := filepath.Join(basePath, "bootstrap.log")
	logOutput := &lumberjack.Logger{
		Filename: path,
		MaxSize:  5,     // 上限到 5MB
		MaxAge:   7,     // 只留 7 天
		Compress: false, // 不壓縮
	}

	log.SetOutput(logOutput)
	logrus.SetOutput(logOutput)

	logrus.SetFormatter(&logrus.TextFormatter{
		TimestampFormat: "2006-01-02 15:04:05",
		FullTimestamp:   true,
		ForceColors:     false, // disable colors for file output
	})

	return logOutput
}

func closeBootstrapLog(logger *lumberjack.Logger) {
	log.SetOutput(io.Discard)
	logrus.SetOutput(io.Discard)
	logger.Close()
}

func init() {
	log.SetOutput(io.Discard)
	logrus.SetOutput(io.Discard)
}
