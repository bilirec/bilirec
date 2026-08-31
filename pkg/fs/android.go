//go:build cgo && android

package fs

/*
#include <stdlib.h>

extern char* fs_call_list_dir(const char* path, char** err_out);
extern void fs_call_notify(const char* path);
*/
import "C"
import (
	"errors"
	iofs "io/fs"
	"os"
	"runtime"
	"unsafe"

	"github.com/bilirec/bilirec/pkg/logger"
)

var log = logger.Named("fs")

func ReadDir(path string) ([]iofs.DirEntry, error) {
	media, mediaErr := listMediaStore(path)
	native, nativeErr := os.ReadDir(path)
	if mediaErr != nil {
		log.Warnf("MediaStore 列目录失败，回退到 os.ReadDir：path=%s err=%v", path, mediaErr)
		return native, nativeErr
	}
	if nativeErr != nil {
		if len(media) > 0 {
			log.Warnf("os.ReadDir 失败，使用 MediaStore 结果：path=%s err=%v", path, nativeErr)
			return media, nil
		}
		return nil, nativeErr
	}
	return mergeDirEntries(native, media), nil
}

func NotifyFileChanged(path string) {
	if path == "" {
		return
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	C.fs_call_notify(cPath)
}

func listMediaStore(path string) ([]iofs.DirEntry, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	var cErr *C.char
	cJSON := C.fs_call_list_dir(cPath, &cErr)
	if cErr != nil {
		err := errors.New(C.GoString(cErr))
		C.free(unsafe.Pointer(cErr))
		if cJSON != nil {
			C.free(unsafe.Pointer(cJSON))
		}
		return nil, err
	}
	if cJSON == nil {
		return nil, errors.New("listDir returned nil")
	}
	defer C.free(unsafe.Pointer(cJSON))
	return parseListJSON(C.GoString(cJSON))
}
