BINARY_NAME := bilirec
BACKEND_PKG := ./cmd/backend
ANDROID_PKG := ./cmd/androidlib

# Android build targets: abi -> goarch and clang binary
ANDROID_TARGETS := arm64-v8a:arm64:aarch64-linux-android21-clang x86_64:amd64:x86_64-linux-android21-clang

.PHONY: dev build android

## dev: run backend in development mode
dev:
	go run $(BACKEND_PKG)

## build: compile backend.exe for Windows
build:
	GOOS=windows go build --buildmode=exe -o ./dist/$(BINARY_NAME)-windows.exe $(BACKEND_PKG)

## android: compile Android shared libraries (.so) for arm64-v8a and x86_64
android:
	@NDK_HOME="$${ANDROID_NDK_LATEST_HOME:-$${ANDROID_NDK_ROOT:-$$ANDROID_NDK_HOME}}"; \
	if [ -z "$$NDK_HOME" ]; then \
		echo "Error: Android NDK not found. Set ANDROID_NDK_LATEST_HOME, ANDROID_NDK_ROOT, or ANDROID_NDK_HOME."; \
		exit 1; \
	fi; \
	for entry in $(ANDROID_TARGETS); do \
		ABI=$$(echo $$entry | cut -d: -f1); \
		GOARCH=$$(echo $$entry | cut -d: -f2); \
		CC_BIN=$$(echo $$entry | cut -d: -f3); \
		CLANG="$$NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin/$$CC_BIN"; \
		if [ ! -x "$$CLANG" ]; then \
			echo "Error: clang not found or not executable: $$CLANG"; \
			exit 1; \
		fi; \
		echo "Building android/$$ABI (GOARCH=$$GOARCH)..."; \
		CC="$$CLANG" GOOS=android GOARCH=$$GOARCH CGO_ENABLED=1 \
			go build -buildmode=c-shared -ldflags "-checklinkname=0" \
			-o ./dist/android/$$ABI/lib$(BINARY_NAME).so $(ANDROID_PKG); \
	done
