BINARY_NAME := bilirec
BACKEND_PKG := ./cmd/backend
ANDROID_PKG := ./cmd/androidlib

# Android build targets: abi -> goarch and clang binary
ANDROID_TARGETS := arm64-v8a:arm64:aarch64-linux-android24-clang x86_64:amd64:x86_64-linux-android24-clang

.PHONY: dev build android

## dev: run backend in development mode
dev:
	go run $(BACKEND_PKG)

## build: compile backend.exe for Windows
build:
	GOOS=windows go build --buildmode=exe -o ./dist/$(BINARY_NAME)-windows.exe $(BACKEND_PKG)

## android: compile Android shared libraries (.so) for arm64-v8a and x86_64
## usage : make android os=windows   (or os=linux)
android:
	@if [ -z "$(os)" ]; then \
		echo "Error: missing required parameter 'os'."; \
		echo "Usage: make android os=windows"; \
		echo "   or: make android os=linux"; \
		exit 1; \
	fi; \
	case "$(os)" in \
		windows) NDK_PREBUILT="windows-x86_64" ;; \
		linux) NDK_PREBUILT="linux-x86_64" ;; \
		*) \
			echo "Error: unsupported os='$(os)'. Expected windows or linux."; \
			exit 1 ;; \
	esac; \
	NDK_HOME="$${ANDROID_NDK_LATEST_HOME:-$${ANDROID_NDK_ROOT:-$$ANDROID_NDK_HOME}}"; \
	if [ -z "$$NDK_HOME" ]; then \
		echo "Error: Android NDK not found. Set ANDROID_NDK_LATEST_HOME, ANDROID_NDK_ROOT, or ANDROID_NDK_HOME."; \
		exit 1; \
	fi; \
	echo "=================================================="; \
	echo "Start building Android shared libraries (.so)"; \
	echo "Host preset (os)  : $(os)"; \
	echo "NDK home          : $$NDK_HOME"; \
	echo "NDK prebuilt      : $$NDK_PREBUILT"; \
	echo "Targets           : $(ANDROID_TARGETS)"; \
	echo "=================================================="; \
	for entry in $(ANDROID_TARGETS); do \
		ABI=$$(echo $$entry | cut -d: -f1); \
		GOARCH=$$(echo $$entry | cut -d: -f2); \
		CC_BIN=$$(echo $$entry | cut -d: -f3); \
		FFMPEGKIT_LIB_DIR="$$(pwd)/lib/$$ABI"; \
		CLANG="$$NDK_HOME/toolchains/llvm/prebuilt/$$NDK_PREBUILT/bin/$$CC_BIN"; \
		if [ ! -x "$$CLANG" ]; then \
			echo "Error: clang not found or not executable: $$CLANG"; \
			exit 1; \
		fi; \
		if [ ! -f "$$FFMPEGKIT_LIB_DIR/libffmpegkit.so" ]; then \
			echo "Error: missing $$FFMPEGKIT_LIB_DIR/libffmpegkit.so"; \
			echo "Expected layout: ./lib/<abi>/libffmpegkit.so"; \
			exit 1; \
		fi; \
		echo "--------------------------------------------------"; \
		echo "Building android/$$ABI"; \
		echo "GOARCH            : $$GOARCH"; \
		echo "Compiler (CC)     : $$CLANG"; \
		echo "FFmpegKit lib dir : $$FFMPEGKIT_LIB_DIR"; \
		echo "Output            : ./dist/android/$$ABI/lib$(BINARY_NAME).so"; \
		echo "--------------------------------------------------"; \
		mkdir -p ./dist/android/$$ABI; \
		CC="$$CLANG" GOOS=android GOARCH=$$GOARCH CGO_ENABLED=1 CGO_LDFLAGS="-L$$FFMPEGKIT_LIB_DIR -lffmpegkit" \
			go build -buildmode=c-shared -ldflags "-checklinkname=0" \
			-o ./dist/android/$$ABI/lib$(BINARY_NAME).so $(ANDROID_PKG); \
	done
