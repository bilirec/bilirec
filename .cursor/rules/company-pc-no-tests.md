# Company PC Safety Rule: No Test Execution

Company PC detection:

- Run `whoami`.
- If the output is exactly `corp\ericlam`, this machine is a company computer.
- When detected as a company computer, apply all rules below strictly.

When working in this repository on a company computer:

- Do not run any test commands under any circumstance.
- Forbidden examples include (but are not limited to):
  - `go test ...`
  - `npm test`
  - `pnpm test`
  - `yarn test`
  - `pytest`
  - `cargo test`
  - any command with `test` intent in scripts or task runners
- If validation is needed, use static analysis and code inspection only.
- If asked to run tests, explicitly decline and explain that company policy forbids test execution on this machine.

This rule has higher priority than optimization or verification preferences.
