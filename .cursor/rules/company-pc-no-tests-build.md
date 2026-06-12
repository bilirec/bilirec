# Company PC Safety Rule: No Test or Build Execution

Company PC detection:

- Run `whoami`.
- If the output is exactly `corp\ericlam`, this machine is a company computer.
- When detected as a company computer, apply all rules below strictly.

When working in this repository on a company computer:

## No Tests

- Do not run any test commands under any circumstance.
- Forbidden examples include (but are not limited to):
  - `go test ...`
  - `npm test`
  - `pnpm test`
  - `yarn test`
  - `pytest`
  - `cargo test`
  - any command with `test` intent in scripts or task runners

## No Build

- Do not run any build or compile commands under any circumstance.
- Forbidden examples include (but are not limited to):
  - `go build ...`
  - `go install ...`
  - `npm run build`
  - `npm build`
  - `pnpm build`
  - `yarn build`
  - `cargo build`
  - `make`
  - `cmake`
  - `tsc` (TypeScript compilation)
  - `webpack`
  - `vite build`
  - any command with `build` or `compile` intent in scripts or task runners

## General

- If validation is needed, use static analysis and code inspection only.
- If asked to run tests or build, explicitly decline and explain that company policy forbids test execution and build compilation on this machine.

This rule has higher priority than optimization or verification preferences.
