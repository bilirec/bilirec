#!/usr/bin/env bash
# Run all benchmarks and memleak tests for a given configuration label.
set -euo pipefail

LABEL="${1:?usage: run_perf_comparison.sh <label> [branch] [perf_preset]}"
BRANCH="${2:-$(git branch --show-current)}"
PRESET="${3:-}"
OUT_DIR="/workspace/bench-results/${LABEL}"
mkdir -p "$OUT_DIR"

echo "=== Config: $LABEL (branch=$BRANCH preset=${PRESET:-none}) ===" | tee "$OUT_DIR/meta.txt"
date -u +"%Y-%m-%dT%H:%M:%SZ" | tee -a "$OUT_DIR/meta.txt"
git rev-parse HEAD | tee -a "$OUT_DIR/meta.txt"
git log -1 --oneline | tee -a "$OUT_DIR/meta.txt"

export BILIBILI_LOGIN_MODE=anonymous
if [[ -n "$PRESET" ]]; then
  export PERF_PRESET="$PRESET"
else
  unset PERF_PRESET || true
fi

echo "PERF_PRESET=${PERF_PRESET:-<unset>}" | tee -a "$OUT_DIR/meta.txt"

# Swagger/docs required for full module graph (CI setup).
mkdir -p docs
if [[ ! -f docs/swagger.json ]]; then
  echo '{}' > docs/swagger.json
fi
if command -v swag >/dev/null 2>&1; then
  swag init -g internal/modules/rest/rest.go -o docs 2>/dev/null || true
fi

BENCH_PKGS=(
  ./internal/processors/...
  ./internal/record_strategies/...
  ./internal/services/stream/...
  ./pkg/hls/...
  ./pkg/pool/...
  ./pkg/rw/...
)

BENCH_ENV=()
if [[ -n "${PERF_PRESET:-}" ]]; then
  BENCH_ENV=(env "PERF_PRESET=$PERF_PRESET")
fi

# --- Benchmarks ---
echo "[$(date -u +%H:%M:%S)] Running benchmarks..." | tee -a "$OUT_DIR/meta.txt"
set +e
"${BENCH_ENV[@]}" go test -v -bench=. -benchmem -run='^$' -timeout 30m "${BENCH_PKGS[@]}" \
  2>&1 | tee "$OUT_DIR/benchmarks.log"
BENCH_EXIT=${PIPESTATUS[0]}
set -e
echo "benchmark_exit=$BENCH_EXIT" >> "$OUT_DIR/meta.txt"

grep -E '^(Benchmark|PASS|FAIL|ok |---)' "$OUT_DIR/benchmarks.log" > "$OUT_DIR/benchmarks_summary.txt" || true
grep -E '(📊|Benchmark:|Iterations:|Wall time:|Per op:|Throughput:|Baseline:|Peak during:|After run:|Peak heap sys:|Total alloc:|GC runs:)' \
  "$OUT_DIR/benchmarks.log" > "$OUT_DIR/benchmarks_reports.txt" || true

# --- Memleak tests ---
echo "[$(date -u +%H:%M:%S)] Running memleak tests..." | tee -a "$OUT_DIR/meta.txt"
set +e
"${BENCH_ENV[@]}" go test -v -count=1 -timeout 2h ./internal/services/recorder \
  -run 'TestRecorder_MemoryLeak|TestRecorder_Goroutine_Leak' \
  2>&1 | tee "$OUT_DIR/memleak.log"
MEM_EXIT=${PIPESTATUS[0]}
set -e
echo "memleak_exit=$MEM_EXIT" >> "$OUT_DIR/meta.txt"

grep -E '(^(=== RUN|--- PASS|--- FAIL|--- SKIP|PASS|FAIL|ok )|📊|📈|✅|⚠️|Memory Analysis|Baseline:|During record:|After stop:|After GC:|Cleanup:|retained|goroutine|性能预设)' \
  "$OUT_DIR/memleak.log" > "$OUT_DIR/memleak_summary.txt" || true

echo "[$(date -u +%H:%M:%S)] Done: $LABEL (bench=$BENCH_EXIT memleak=$MEM_EXIT)" | tee -a "$OUT_DIR/meta.txt"
exit $(( BENCH_EXIT != 0 || MEM_EXIT != 0 ? 1 : 0 ))
