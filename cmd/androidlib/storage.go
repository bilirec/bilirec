//go:build cgo && android

package main

/*
#include <jni.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>

static JavaVM* jvm;
static jclass bridge_class;
static jmethodID list_dir_mid;
static jmethodID notify_mid;
static pthread_mutex_t mu = PTHREAD_MUTEX_INITIALIZER;

static int jni_check_exception(JNIEnv* env, char** err_out) {
	if (!(*env)->ExceptionCheck(env)) {
		return 0;
	}
	(*env)->ExceptionDescribe(env);
	(*env)->ExceptionClear(env);
	if (err_out != NULL) {
		*err_out = strdup("jni exception");
	}
	return 1;
}

static int fs_cache_from_class(JNIEnv* env, jclass clazz, char** err_out) {
	jclass global = (*env)->NewGlobalRef(env, clazz);
	if (global == NULL) {
		if (err_out != NULL) {
			*err_out = strdup("NewGlobalRef failed");
		}
		return -1;
	}
	jmethodID list_id = (*env)->GetStaticMethodID(env, clazz, "listDir", "(Ljava/lang/String;)Ljava/lang/String;");
	if (jni_check_exception(env, err_out) || list_id == NULL) {
		(*env)->DeleteGlobalRef(env, global);
		if (err_out != NULL && *err_out == NULL) {
			*err_out = strdup("GetStaticMethodID listDir failed");
		}
		return -1;
	}
	jmethodID notify_id = (*env)->GetStaticMethodID(env, clazz, "notifyFileChanged", "(Ljava/lang/String;)V");
	if (jni_check_exception(env, err_out) || notify_id == NULL) {
		(*env)->DeleteGlobalRef(env, global);
		if (err_out != NULL && *err_out == NULL) {
			*err_out = strdup("GetStaticMethodID notifyFileChanged failed");
		}
		return -1;
	}
	pthread_mutex_lock(&mu);
	if (bridge_class != NULL) {
		(*env)->DeleteGlobalRef(env, bridge_class);
	}
	bridge_class = global;
	list_dir_mid = list_id;
	notify_mid = notify_id;
	pthread_mutex_unlock(&mu);
	return 0;
}

void fs_native_register(JNIEnv* env, jclass clazz) {
	if (env == NULL || clazz == NULL) {
		return;
	}
	(*env)->GetJavaVM(env, &jvm);
	fs_cache_from_class(env, clazz, NULL);
}

static int fs_get_env(JNIEnv** env, int* attached) {
	*attached = 0;
	if (jvm == NULL) {
		JavaVM* vms[1];
		jsize n = 0;
		if (JNI_GetCreatedJavaVMs(vms, 1, &n) != JNI_OK || n < 1) {
			return -1;
		}
		jvm = vms[0];
	}
	jint rc = (*jvm)->GetEnv(jvm, (void**)env, JNI_VERSION_1_6);
	if (rc == JNI_OK) {
		return 0;
	}
	if (rc != JNI_EDETACHED) {
		return -1;
	}
	if ((*jvm)->AttachCurrentThread(jvm, env, NULL) != JNI_OK) {
		return -1;
	}
	*attached = 1;
	return 0;
}

static int fs_ensure_bridge(JNIEnv* env, char** err_out) {
	pthread_mutex_lock(&mu);
	int ready = bridge_class != NULL && list_dir_mid != NULL && notify_mid != NULL;
	pthread_mutex_unlock(&mu);
	if (ready) {
		return 0;
	}

	jclass at_cls = (*env)->FindClass(env, "android/app/ActivityThread");
	if (jni_check_exception(env, err_out) || at_cls == NULL) {
		if (err_out != NULL && *err_out == NULL) {
			*err_out = strdup("FindClass ActivityThread failed");
		}
		return -1;
	}
	jmethodID current_app = (*env)->GetStaticMethodID(env, at_cls, "currentApplication", "()Landroid/app/Application;");
	if (jni_check_exception(env, err_out) || current_app == NULL) {
		(*env)->DeleteLocalRef(env, at_cls);
		if (err_out != NULL && *err_out == NULL) {
			*err_out = strdup("currentApplication method missing");
		}
		return -1;
	}
	jobject app = (*env)->CallStaticObjectMethod(env, at_cls, current_app);
	(*env)->DeleteLocalRef(env, at_cls);
	if (jni_check_exception(env, err_out) || app == NULL) {
		if (err_out != NULL && *err_out == NULL) {
			*err_out = strdup("currentApplication returned null");
		}
		return -1;
	}

	jclass app_cls = (*env)->GetObjectClass(env, app);
	jmethodID get_loader = (*env)->GetMethodID(env, app_cls, "getClassLoader", "()Ljava/lang/ClassLoader;");
	jobject loader = (*env)->CallObjectMethod(env, app, get_loader);
	(*env)->DeleteLocalRef(env, app_cls);
	(*env)->DeleteLocalRef(env, app);
	if (jni_check_exception(env, err_out) || loader == NULL) {
		if (err_out != NULL && *err_out == NULL) {
			*err_out = strdup("getClassLoader failed");
		}
		return -1;
	}

	jclass loader_cls = (*env)->FindClass(env, "java/lang/ClassLoader");
	jmethodID load_class = (*env)->GetMethodID(env, loader_cls, "loadClass", "(Ljava/lang/String;)Ljava/lang/Class;");
	jstring name = (*env)->NewStringUTF(env, "org.bilirec.bilirec.StorageBridge");
	jclass bridge = (jclass)(*env)->CallObjectMethod(env, loader, load_class, name);
	(*env)->DeleteLocalRef(env, loader_cls);
	(*env)->DeleteLocalRef(env, loader);
	(*env)->DeleteLocalRef(env, name);
	if (jni_check_exception(env, err_out) || bridge == NULL) {
		if (err_out != NULL && *err_out == NULL) {
			*err_out = strdup("loadClass StorageBridge failed");
		}
		return -1;
	}

	int rc = fs_cache_from_class(env, bridge, err_out);
	(*env)->DeleteLocalRef(env, bridge);
	return rc;
}

char* fs_call_list_dir(const char* path, char** err_out) {
	JNIEnv* env;
	int attached = 0;
	if (fs_get_env(&env, &attached) != 0) {
		*err_out = strdup("GetEnv failed");
		return NULL;
	}
	if (fs_ensure_bridge(env, err_out) != 0) {
		if (attached) {
			(*jvm)->DetachCurrentThread(jvm);
		}
		return NULL;
	}

	pthread_mutex_lock(&mu);
	jclass cls = bridge_class;
	jmethodID mid = list_dir_mid;
	pthread_mutex_unlock(&mu);

	jstring jpath = (*env)->NewStringUTF(env, path);
	if (jpath == NULL) {
		*err_out = strdup("NewStringUTF failed");
		if (attached) {
			(*jvm)->DetachCurrentThread(jvm);
		}
		return NULL;
	}
	jstring jret = (jstring)(*env)->CallStaticObjectMethod(env, cls, mid, jpath);
	(*env)->DeleteLocalRef(env, jpath);
	if (jni_check_exception(env, err_out) || jret == NULL) {
		if (err_out != NULL && *err_out == NULL) {
			*err_out = strdup("listDir returned null");
		}
		if (attached) {
			(*jvm)->DetachCurrentThread(jvm);
		}
		return NULL;
	}

	const char* utf = (*env)->GetStringUTFChars(env, jret, NULL);
	char* copy = utf != NULL ? strdup(utf) : NULL;
	if (utf != NULL) {
		(*env)->ReleaseStringUTFChars(env, jret, utf);
	}
	(*env)->DeleteLocalRef(env, jret);
	if (attached) {
		(*jvm)->DetachCurrentThread(jvm);
	}
	if (copy == NULL) {
		*err_out = strdup("listDir empty string");
	}
	return copy;
}

void fs_call_notify(const char* path) {
	JNIEnv* env;
	int attached = 0;
	if (fs_get_env(&env, &attached) != 0) {
		return;
	}
	if (fs_ensure_bridge(env, NULL) != 0) {
		if (attached) {
			(*jvm)->DetachCurrentThread(jvm);
		}
		return;
	}

	pthread_mutex_lock(&mu);
	jclass cls = bridge_class;
	jmethodID mid = notify_mid;
	pthread_mutex_unlock(&mu);

	jstring jpath = (*env)->NewStringUTF(env, path);
	if (jpath == NULL) {
		if (attached) {
			(*jvm)->DetachCurrentThread(jvm);
		}
		return;
	}
	(*env)->CallStaticVoidMethod(env, cls, mid, jpath);
	(*env)->ExceptionClear(env);
	(*env)->DeleteLocalRef(env, jpath);
	if (attached) {
		(*jvm)->DetachCurrentThread(jvm);
	}
}
*/
import "C"
