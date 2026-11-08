---
name: bilirec-pprof
description: >
  Pull Go pprof profiles (heap, allocs, goroutine, CPU) from a running BiliRec
  (http://<BILIREC_HOST>:<API_PORT>/debug/pprof) and analyze memory/CPU. Finds the per-process
  pprof token in the log backend via Grafana MCP, correlates with process_*/go_memstats_* metrics.
  Use when the user asks about BiliRec memory usage, RSS too high, idle memory, GC churn,
  goroutine leaks, CPU hot paths, or says "pprof" / "heap profile" / "記憶體飄高".
---

# BiliRec pprof

BiliRec exposes `/debug/pprof` on the **main API port** (`<BILIREC_HOST>:<API_PORT>`, not the
separate metrics port). It is only mounted when `DEBUG` is on. Auth is a **random 32-byte hex token
regenerated on every process start**, sent as the raw `Authorization` header value (no `Bearer `
prefix). Source: `internal/modules/rest/rest.go`.

`<BILIREC_HOST>` / `<API_PORT>` / datasource UIDs below are placeholders — fill in the deployment
you are inspecting. Start by reading the `query-local-metrics` skill (`references/bilirec.md`) for
metric names; use Grafana MCP namespace `user-grafana`.

## 1. Find the token (current process only)

The token is logged once at startup by logger `rest` in the log backend (datasource
`<VICTORIALOGS_DATASOURCE_UID>`, LogsQL):

```
query_loki_logs datasourceUid=<VICTORIALOGS_DATASOURCE_UID>
  logql: "/debug/pprof"
  startRfc3339: now-30d  endRfc3339: now  limit: 5  format: compact
```

Line looks like: `你可以使用十六进制令牌 "<64 hex>" 登录 /debug/pprof`. Results are newest first.

**Verify it belongs to the live process**: the newest line's timestamp (ns) / 1e9 must equal
`process_start_time_seconds{job="bilirec"}` (instant query on `<PROMETHEUS_DATASOURCE_UID>`). If it
is older, the process restarted without logs shipped, or `DEBUG` is off (then `/debug/pprof` returns
404). Never hard-code or store the token; it changes on every restart.

## 2. Fetch profiles (PowerShell, Windows)

Use `curl.exe` (not the `curl` alias). Save under `$env:TEMP\bilirec-pprof`.

```powershell
$h = "<BILIREC_HOST>:<API_PORT>"
$t = "<token>"; $d = "$env:TEMP\bilirec-pprof"; New-Item -ItemType Directory -Force $d | Out-Null
foreach ($p in "heap","allocs","goroutine") {
  curl.exe -s -m 30 -H "Authorization: $t" "http://$h/debug/pprof/$p" -o "$d\$p.pb.gz" -w "$p %{http_code} %{size_download}`n"
}
# MemStats block (HeapInuse/HeapIdle/HeapReleased/NextGC/NumGC) is in the debug=1 text form
curl.exe -s -m 30 -H "Authorization: $t" "http://$h/debug/pprof/heap?debug=1" -o "$d\heap.txt"
# CPU: blocks for N seconds
curl.exe -s -m 60 -H "Authorization: $t" "http://$h/debug/pprof/profile?seconds=20" -o "$d\cpu.pb.gz"
```

`401` = wrong/stale token. `404` = debug mode off. Each fetch can take several seconds over the
network.

## 3. Analyze

```powershell
go tool pprof -sample_index=inuse_space -top -nodecount=30 "$d\heap.pb.gz"     # live (as of last GC)
go tool pprof -sample_index=alloc_space -top -nodecount=15 "$d\allocs.pb.gz"   # cumulative churn since start
go tool pprof -sample_index=inuse_space -peek "<func regex>" "$d\heap.pb.gz"   # who calls a hot allocator
go tool pprof -sample_index=alloc_space -list "<func regex>" "$d\allocs.pb.gz" # line-level
Select-String "$d\heap.txt" -Pattern "^# (HeapAlloc|HeapSys|HeapIdle|HeapInuse|HeapReleased|NextGC|NumGC|Sys) "
go tool pprof -top "$d\goroutine.pb.gz"
```

Notes:
- inuse profile is a snapshot at the **last completed GC**, so it excludes garbage that `HeapAlloc` still counts.
- Heap profile only shows Go heap. RSS = Go heap in use + runtime overhead + file-mapped binary (`process_resident_memory_file_bytes`, ~28 MB).
- Compare **idle vs recording** with metrics (pprof can only show "now"): query at a past instant with an absolute RFC3339 `endTime` (`now-X` forms are fine for instant; range queries need relative `startTime` like `now-3h`, no mixed digit strings).

Memory metrics on `<PROMETHEUS_DATASOURCE_UID>`, `job="bilirec"`:
`process_resident_memory_anon_bytes`, `process_resident_memory_file_bytes`, `go_memstats_alloc_bytes`,
`go_memstats_next_gc_bytes`, `go_memstats_heap_idle_bytes`, `go_memstats_heap_released_bytes`,
`go_memstats_heap_sys_bytes`, `go_memstats_heap_objects`, `go_goroutines`, `go_gc_gogc_percent`,
`go_gc_gomemlimit_bytes`, `bilirec_active_recordings`.

## 4. Interpret (BiliRec specifics)

- **Idle live heap (`go_memstats_alloc_bytes` with 0 recordings) not near zero** → buffers retained by bounded pools
  (`pkg/pool` BucketedBytesPool, `WithPoolBoundedMode(true)` keeps buffers in a channel, so GC and the janitor's
  `FreeOSMemory` cannot reclaim them). Default-tier pools in `internal/services/stream` and
  `internal/record_strategies` live forever; only the high-quality tier is dropped after 15 min idle (`LazyDualPool`).
- **GC headroom**: with `GOGC=100`, next GC at about 2x live heap, so RSS peaks ~live + live while recording.
  `go_memstats_next_gc_bytes` shows the ceiling; there is no `GOMEMLIMIT` by default.
- **Allocation churn**: `pkg/flv.(*RealtimeFixer).Fix` copies output into a fresh `[]byte` per call; it dominated `alloc_space`.
- Per-recording fixed cost: `LIVE_STREAM_WRITER_BUFFER_SIZE` (default 8 MB) via `rw.NewFlushLockedBufferedWriter`.
- Janitor (`internal/services/janitor`) runs `runtime.GC()` x2 + `debug.FreeOSMemory()` once after idle grace; it does not drop pool-retained buffers.

Response shape: lead with the root cause, then idle vs recording numbers, then the pprof evidence (top entries), then fixes ranked by effort.
