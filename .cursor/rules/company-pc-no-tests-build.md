---
description: On company PC (corp\ericlam), forbid native .exe-producing build and test commands to avoid antivirus false positives
alwaysApply: true
---

# Company PC Safety Rule: No Native .exe Build or Test

**Why:** On this machine, compiling or running tests that produce Windows `.exe` binaries can trigger corporate antivirus false positives. Only restrict commands that emit native executables—not web builds, linters, or other non-exe tooling.

Company PC detection:

- Run `whoami`.
- If the output is exactly `corp\ericlam`, this machine is a company computer.
- When detected as a company computer, apply all rules below strictly.

When working in this repository on a company computer:

## No Native-Executable Tests

- Do not run test commands that compile native binaries (`.exe` on Windows).
- Forbidden examples include (but are not limited to):
  - `go test ...` (compiles temporary `*.test.exe` on Windows)
  - `cargo test`
  - `ctest` / native C/C++ test runners that link executables
  - any test runner that writes a `.exe` to disk

## No Native-Executable Builds

- Do not run build or compile commands that output Windows `.exe` files.
- Forbidden examples include (but are not limited to):
  - `go build ...`
  - `go install ...`
  - `cargo build`
  - `make build` (this repo’s target produces `bilirec-windows.exe`)
  - `cmake --build ...` / `make` when the artifact is a native `.exe`
  - any command with native compile intent whose output is a `.exe`

## Allowed (not blocked by this rule)

- Frontend / asset builds that do not emit `.exe` (e.g. `pnpm build`, `vite build`, `tsc`, `webpack`).
- JS/TS test runners that do not compile native binaries (e.g. `pnpm test`, `vitest`, `jest`).
- Static analysis and inspection: `go vet`, linters, formatters, code review, reading source.
- `go run ...` is discouraged on company PC if it still triggers AV; prefer static checks unless the user explicitly accepts the risk.
- Builds for non-`.exe` artifacts (e.g. `make android` → `.so`) are outside this rule’s scope.

## General

- If validation is needed, prefer static analysis and code inspection.
- If asked to run a forbidden native build or test, decline and explain that compiling `.exe` on this company PC risks antivirus false positives.
- Other builds and tests that do not produce `.exe` may still be run when appropriate.

This rule has higher priority than optimization or verification preferences when native `.exe` output is involved.
