#!/usr/bin/env bash
set -euo pipefail
cd /workspace

go install github.com/swaggo/swag/cmd/swag@v1.16.6 2>/dev/null || true
export PATH="$(go env GOPATH)/bin:$PATH"

echo "=== 1/3 main branch ==="
git checkout main
./scripts/run_perf_comparison.sh main-branch main "" || echo "main-branch had failures (continuing)"

echo "=== 2/3 feat/optimize-perf low-cpu ==="
git checkout feat/optimize-perf
./scripts/run_perf_comparison.sh low-cpu feat/optimize-perf low-cpu || echo "low-cpu had failures (continuing)"

echo "=== 3/3 feat/optimize-perf low-mem ==="
./scripts/run_perf_comparison.sh low-mem feat/optimize-perf low-mem || echo "low-mem had failures (continuing)"

echo "ALL DONE" | tee /workspace/bench-results/COMPLETE
