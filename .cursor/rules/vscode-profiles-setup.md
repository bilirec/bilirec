---
description: Create and switch Android cgo, Windows, and Darwin VS Code profiles with auto-detected local toolchain paths
alwaysApply: true
---

# VS Code Profiles Setup Rule (android-cgo / windows / darwin)

When working in this repository, keep three VS Code profile files under `.vscode/` and provide tasks to switch between them.

If any file does not exist, create it.

- `.vscode/settings.android-cgo.json`
- `.vscode/settings.windows.json`
- `.vscode/settings.darwin.json`
- `.vscode/tasks.json`

## Path detection requirements

Do not hardcode guessed paths.
Always detect toolchain paths from the current machine first, then write them into profile files.

### Detect Android NDK clang path (Windows PowerShell)

1. Prefer environment variables first.
2. Only if env vars are unavailable, fallback to scanning common SDK locations.
3. Prefer `aarch64-linux-android24-clang.cmd`.
4. Use the newest NDK version if multiple versions exist.

Reference command:

```powershell
$candidateRoots = @()

# Highest priority: explicit NDK env vars
if ($env:ANDROID_NDK_HOME) { $candidateRoots += $env:ANDROID_NDK_HOME }
if ($env:ANDROID_NDK_ROOT) { $candidateRoots += $env:ANDROID_NDK_ROOT }

# Next: SDK env vars (derive NDK root from sdk\ndk\*)
if ($env:ANDROID_SDK_ROOT) {
  $candidateRoots += (Get-ChildItem (Join-Path $env:ANDROID_SDK_ROOT "ndk\*") -ErrorAction SilentlyContinue | Select-Object -ExpandProperty FullName)
}
if ($env:ANDROID_HOME) {
  $candidateRoots += (Get-ChildItem (Join-Path $env:ANDROID_HOME "ndk\*") -ErrorAction SilentlyContinue | Select-Object -ExpandProperty FullName)
}

# Fallback only
$localSdkRoot = Join-Path $env:LOCALAPPDATA "Android\Sdk"
$candidateRoots += (Get-ChildItem (Join-Path $localSdkRoot "ndk\*") -ErrorAction SilentlyContinue | Select-Object -ExpandProperty FullName)

$candidateRoots = $candidateRoots | Where-Object { $_ } | Select-Object -Unique

$ndkClang = foreach ($root in $candidateRoots) {
  $bin = Join-Path $root "toolchains\llvm\prebuilt\windows-x86_64\bin\aarch64-linux-android24-clang.cmd"
  if (Test-Path $bin) { $bin }
} | Sort-Object -Descending | Select-Object -First 1

$ndkClangXX = $ndkClang -replace "-clang\.cmd$", "-clang++.cmd"
```

### Detect Go tool directory for cgo helper path

Reference command:

```powershell
$goroot = go env GOROOT
$goToolDir = Join-Path $goroot "pkg\tool\windows_amd64"
```

## Profile content requirements

### 1) android-cgo profile

File: `.vscode/settings.android-cgo.json`

- Set `go.buildTags` to `android`.
- Set `go.toolsEnvVars`:
  - `GOOS=android`
  - `GOARCH=arm64`
  - `CGO_ENABLED=1`
  - `CC=<detected aarch64-linux-android24-clang.cmd>`
  - `CXX=<detected aarch64-linux-android24-clang++.cmd>`
  - `GOTOOLDIR=<detected go tool dir>`
- Do not set custom `CGO_CFLAGS` or `CGO_LDFLAGS` target triplets unless explicitly required by the user.

### 2) windows profile

File: `.vscode/settings.windows.json`

- Set `go.buildTags` to `""` (must clear android tag inheritance).
- Set `go.toolsEnvVars`:
  - `GOOS=windows`
  - `GOARCH=amd64`
  - `CGO_ENABLED=1`
  - `CC=""`
  - `CXX=""`
  - `GOTOOLDIR=<detected go tool dir>`

### 3) darwin profile

File: `.vscode/settings.darwin.json`

- Set `go.buildTags` to `""` (must clear android tag inheritance).
- Set `go.toolsEnvVars`:
  - `GOOS=darwin`
  - `GOARCH=arm64` (default target; change only if user requests amd64)
  - `CGO_ENABLED=0` (avoid Android clang collision during darwin analysis on non-darwin hosts)
  - `CC=""`
  - `CXX=""`
  - `GOTOOLDIR=<detected go tool dir>`

## Switch tasks requirements

Ensure `.vscode/tasks.json` includes tasks:

- `Profile: Switch to android(cgo)`
- `Profile: Switch to windows`
- `Profile: Switch to darwin`

Each task must copy the matching profile file to `.vscode/settings.json` and print a reload hint.

Reference pattern:

```powershell
Copy-Item -Force "${workspaceFolder}\.vscode\settings.<profile>.json" "${workspaceFolder}\.vscode\settings.json"
```

## Anti-collision rules

- Only Android profile may define Android NDK `CC/CXX`.
- Windows and Darwin profiles must clear `CC/CXX` (`""`).
- After switching profile, reload VS Code window so gopls and cgo env are refreshed.
