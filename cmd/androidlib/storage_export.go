//go:build cgo && android

package main

/*
#include <jni.h>

extern void fs_native_register(JNIEnv* env, jclass clazz);
*/
import "C"

//export Java_org_bilirec_bilirec_StorageBridge_nativeRegister
func Java_org_bilirec_bilirec_StorageBridge_nativeRegister(env *C.JNIEnv, clazz C.jclass) {
	C.fs_native_register(env, clazz)
}
