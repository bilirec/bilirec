//go:build cgo && android

package main

/*
#include <sys/system_properties.h>
#include <stdlib.h>
*/
import "C"
import (
	"os"
	"time"
	_ "time/tzdata"
	"unsafe"

	"github.com/sirupsen/logrus"
)

func loadAndroidTimeZone() {
	// 配置一塊 C 記憶體來接收屬性值
	// PROP_VALUE_MAX 預設通常是 92，足以容納時區字串
	var propValue [C.PROP_VALUE_MAX]C.char

	// 準備要查詢的 Android 屬性鍵值
	propName := C.CString("persist.sys.timezone")
	defer C.free(unsafe.Pointer(propName))

	// 呼叫 Android 系統底層 API 讀取屬性
	length := C.__system_property_get(propName, &propValue[0])

	if length > 0 {
		// 將 C 語言字串轉換回 Go 語言字串
		tzName := C.GoString(&propValue[0])
		logrus.Infof("成功從 Android 系統底層讀取到時區: %s", tzName)

		// 載入並設定時區
		loc, err := time.LoadLocation(tzName)
		if err != nil {
			logrus.Infof("載入時區失敗: %v, 將退回 UTC", err)
		} else {
			time.Local = loc
			logrus.Infof("time.Local 已成功設定為: %s", loc.String())
		}
	} else {
		logrus.Info("無法讀取 persist.sys.timezone，可能因權限或環境限制，將退回 UTC")
	}
}

func loadEnvironmentTimeZone() {
	tzEnv, exists := os.LookupEnv("TZ")
	if exists {
		if tzEnv == "" {
			// POSIX 標準：如果 TZ 被明確設為空字串，通常代表強制使用 UTC
			time.Local = time.UTC
			logrus.Info("TZ 環境變數為空，強制設為 UTC")
			return
		}

		// 嘗試套用 TZ 環境變數
		loc, err := time.LoadLocation(tzEnv)
		if err == nil {
			time.Local = loc
			logrus.Infof("套用 TZ 環境變數成功: %s", loc.String())
			return
		}
		logrus.Infof("TZ 環境變數 (%s) 解析失敗", tzEnv)
	}
}
