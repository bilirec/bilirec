//go:build cgo && android

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testHost      = "127.0.0.1"
	testPort      = 8080
	testAddr      = "127.0.0.1:8080"
	frontendURL   = "https://app.bilirec.org"
	stateWaitOpen = 5 * time.Second
	stateWaitOff  = 5 * time.Second
)

type restartStats struct {
	rounds       int
	startFailed  int
	pseudoStart  int
	httpTimeout  int
	stopFailed   int
	closeFailed  int
	maxStartCost time.Duration
	maxStopCost  time.Duration
}

type concurrentWaveStats struct {
	rounds          int
	workers         int
	startNonZero    int
	stopNonZero     int
	portOpenFailed  int
	portCloseFailed int
	httpTimeout     int
}

type reproScriptStats struct {
	iterations           int
	stopNonZero          atomic.Int64
	stopSlow             atomic.Int64
	startNonZero         atomic.Int64
	pseudoStart          atomic.Int64
	httpTimeout          atomic.Int64
	hammerTimeout        atomic.Int64
	sseDialErrors        atomic.Int64
	maxStopCost          time.Duration
	maxStartCost         time.Duration
	maxSSEActiveObserved int64
}

type cycleOptions struct {
	rounds        int
	interRoundGap time.Duration
	doubleStart   bool
	doubleStop    bool
	openWait      time.Duration
	closeWait     time.Duration
}

func TestRealInstanceFrequentStartStop_Port8080(t *testing.T) {
	//	skipIfCI(t)

	if testing.Short() {
		t.Skip("skip real-instance restart stress in short mode")
	}
	ensurePort8080Available(t)

	// Ensure global singleton state is clean before and after the test.
	_ = Stop()
	t.Cleanup(func() {
		_ = Stop()
	})

	stats := runRealInstanceCycles(t, t.TempDir(), cycleOptions{
		rounds:        15,
		interRoundGap: 80 * time.Millisecond,
		doubleStart:   false,
		doubleStop:    false,
		openWait:      stateWaitOpen,
		closeWait:     stateWaitOff,
	})
	logRestartStats(t, stats)

	if stats.startFailed > 0 || stats.pseudoStart > 0 || stats.httpTimeout > 0 || stats.stopFailed > 0 || stats.closeFailed > 0 {
		t.Fatalf("restart stress detected anomalies: start_failed=%d pseudo_start=%d http_timeout=%d stop_failed=%d port_close_failed=%d", stats.startFailed, stats.pseudoStart, stats.httpTimeout, stats.stopFailed, stats.closeFailed)
	}
}

func TestRealInstanceFrequentStartStop_Port8080_Metrics(t *testing.T) {
	//	skipIfCI(t)
	ensurePort8080Available(t)

	_ = Stop()
	t.Cleanup(func() {
		_ = Stop()
	})

	stats := runRealInstanceCycles(t, t.TempDir(), cycleOptions{
		rounds:        30,
		interRoundGap: 50 * time.Millisecond,
		doubleStart:   false,
		doubleStop:    false,
		openWait:      stateWaitOpen,
		closeWait:     stateWaitOff,
	})
	logRestartStats(t, stats)

	if stats.pseudoStart > 0 || stats.httpTimeout > 0 {
		t.Fatalf("metrics run detected pseudo_start=%d http_timeout=%d", stats.pseudoStart, stats.httpTimeout)
	}
}

func TestRealInstanceFrequentStartStop_Port8080_Aggressive(t *testing.T) {
	//	skipIfCI(t)
	ensurePort8080Available(t)

	_ = Stop()
	t.Cleanup(func() {
		_ = Stop()
	})

	stats := runRealInstanceCycles(t, t.TempDir(), cycleOptions{
		rounds:        80,
		interRoundGap: 0,
		doubleStart:   true,
		doubleStop:    true,
		openWait:      2500 * time.Millisecond,
		closeWait:     2500 * time.Millisecond,
	})
	logRestartStats(t, stats)

	if stats.startFailed > 0 || stats.pseudoStart > 0 || stats.httpTimeout > 0 || stats.stopFailed > 0 || stats.closeFailed > 0 {
		t.Fatalf("aggressive restart stress detected anomalies: start_failed=%d pseudo_start=%d http_timeout=%d stop_failed=%d port_close_failed=%d", stats.startFailed, stats.pseudoStart, stats.httpTimeout, stats.stopFailed, stats.closeFailed)
	}
}

func TestRealInstanceConcurrentStartStop_Port8080_HighConcurrency(t *testing.T) {
	//	skipIfCI(t)
	ensurePort8080Available(t)

	_ = Stop()
	t.Cleanup(func() {
		_ = Stop()
	})

	const (
		rounds  = 20
		workers = 24
	)

	basePath, err := os.MkdirTemp("", "bilirec-androidlib-repro-")
	if err != nil {
		t.Fatalf("create repro temp dir failed: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := os.RemoveAll(basePath); cleanupErr != nil {
			t.Logf("repro temp dir cleanup skipped due lock: %v", cleanupErr)
		}
	})
	cfg := StartConfig{
		BasePath:    basePath,
		Host:        testHost,
		Port:        testPort,
		FrontendURL: frontendURL,
		OutputDir:   filepath.Join(basePath, "records"),
		SSEToken:    "test-token",
	}

	stats := concurrentWaveStats{rounds: rounds, workers: workers}

	for i := 1; i <= rounds; i++ {
		t.Logf("round %d: concurrent start wave begin (workers=%d)", i, workers)
		startCodes := runConcurrentCalls(workers, func() int { return int(start(cfg)) })
		for _, code := range startCodes {
			if code != 0 {
				stats.startNonZero++
			}
		}

		if err := waitForTCPState(testAddr, true, 4*time.Second); err != nil {
			stats.portOpenFailed++
			t.Logf("round %d: port open check failed after start wave: %v", i, err)
		}

		timedOut, err := probeHTTPNoTimeout("http://localhost:8080", 1200*time.Millisecond)
		if timedOut {
			stats.httpTimeout++
			t.Logf("round %d: HTTP probe timeout after start wave: %v", i, err)
		} else if err != nil {
			t.Logf("round %d: HTTP probe non-timeout error after start wave (ignored): %v", i, err)
		}

		t.Logf("round %d: concurrent stop wave begin (workers=%d)", i, workers)
		stopCodes := runConcurrentCalls(workers, func() int { return int(Stop()) })
		for _, code := range stopCodes {
			if code != 0 {
				stats.stopNonZero++
			}
		}

		if err := waitForTCPState(testAddr, false, 4*time.Second); err != nil {
			stats.portCloseFailed++
			t.Logf("round %d: port close check failed after stop wave: %v", i, err)
		}

		t.Logf("round %d: concurrent waves done", i)
	}

	t.Logf("concurrent wave stats: rounds=%d workers=%d start_non_zero=%d stop_non_zero=%d port_open_failed=%d port_close_failed=%d http_timeout=%d",
		stats.rounds,
		stats.workers,
		stats.startNonZero,
		stats.stopNonZero,
		stats.portOpenFailed,
		stats.portCloseFailed,
		stats.httpTimeout,
	)

	if stats.startNonZero > 0 || stats.stopNonZero > 0 || stats.portOpenFailed > 0 || stats.portCloseFailed > 0 || stats.httpTimeout > 0 {
		t.Fatalf("high-concurrency real-instance test detected anomalies: start_non_zero=%d stop_non_zero=%d port_open_failed=%d port_close_failed=%d http_timeout=%d",
			stats.startNonZero,
			stats.stopNonZero,
			stats.portOpenFailed,
			stats.portCloseFailed,
			stats.httpTimeout,
		)
	}
}

// 僞啓動重啓腳本，模擬 app 端使用者頻繁點擊啓動按鈕的行爲，並在此過程中持續施加 HTTP 和 SSE 壓力，觀察是否能重現過去遇到的假啓動（pseudo-start）問題。
func TestRealInstanceReproScript_PseudoStartReproduce(t *testing.T) {
	//	skipIfCI(t)
	if testing.Short() {
		t.Skip("skip repro script in short mode")
	}
	ensurePort8080Available(t)

	_ = Stop()
	t.Cleanup(func() {
		_ = Stop()
	})

	basePath, err := os.MkdirTemp("", "bilirec-androidlib-repro-")
	if err != nil {
		t.Fatalf("create repro temp dir failed: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := os.RemoveAll(basePath); cleanupErr != nil {
			t.Logf("repro temp dir cleanup skipped due lock: %v", cleanupErr)
		}
	})
	cfg := StartConfig{
		BasePath:    basePath,
		Host:        testHost,
		Port:        testPort,
		FrontendURL: frontendURL,
		OutputDir:   filepath.Join(basePath, "records"),
		SSEToken:    "test-token",
	}

	if code := start(cfg); code != 0 {
		t.Fatalf("initial start failed: code=%d", code)
	}
	if err := waitForTCPState(testAddr, true, 5*time.Second); err != nil {
		t.Fatalf("initial port open check failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	var stats reproScriptStats
	var wg sync.WaitGroup

	activeSSE := spawnSSEPressure(ctx, &wg, 20, &stats)
	spawnHTTPHammer(ctx, &wg, 8, &stats)

	rnd := rand.New(rand.NewSource(42))
	deadline := time.Now().Add(40 * time.Second)
	for iter := 1; time.Now().Before(deadline); iter++ {
		stats.iterations = iter

		// Model app-side single-button behavior: no concurrent start bursts.
		// Sequence per iteration: start -> stop -> immediate start -> immediate stop.
		t.Logf("repro iter %d: start", iter)
		startAt := time.Now()
		startCode := int(start(cfg))
		startCost := time.Since(startAt)
		if startCost > stats.maxStartCost {
			stats.maxStartCost = startCost
		}
		if startCode != 0 {
			stats.startNonZero.Add(1)
		} else {
			if err := waitForTCPState(testAddr, true, 1500*time.Millisecond); err != nil {
				stats.pseudoStart.Add(1)
				t.Logf("repro iter %d: start returned %d but port open failed: %v", iter, startCode, err)
			}

			timedOut, err := probeHTTPNoTimeout("http://localhost:8080", 1200*time.Millisecond)
			if timedOut {
				stats.httpTimeout.Add(1)
				t.Logf("repro iter %d: HTTP timeout after start: %v", iter, err)
			}
		}

		t.Logf("repro iter %d: stop", iter)
		stopAt := time.Now()
		stopCode := int(Stop())
		stopCost := time.Since(stopAt)
		if stopCost > stats.maxStopCost {
			stats.maxStopCost = stopCost
		}
		if stopCode != 0 {
			stats.stopNonZero.Add(1)
		}
		if stopCost >= 12*time.Second {
			stats.stopSlow.Add(1)
		}

		t.Logf("repro iter %d: immediate restart after stop (stop_code=%d stop_cost=%v)", iter, stopCode, stopCost)
		quickStartAt := time.Now()
		quickStartCode := int(start(cfg))
		quickStartCost := time.Since(quickStartAt)
		if quickStartCost > stats.maxStartCost {
			stats.maxStartCost = quickStartCost
		}
		if quickStartCode != 0 {
			stats.startNonZero.Add(1)
		} else {
			if err := waitForTCPState(testAddr, true, 2500*time.Millisecond); err != nil {
				stats.pseudoStart.Add(1)
				t.Logf("repro iter %d: pseudo-start after immediate restart: %v", iter, err)
			}
			timedOut, err := probeHTTPNoTimeout("http://localhost:8080", 1200*time.Millisecond)
			if timedOut {
				stats.httpTimeout.Add(1)
				t.Logf("repro iter %d: HTTP timeout after immediate restart: %v", iter, err)
			}
		}

		time.Sleep(time.Duration(5+rnd.Intn(20)) * time.Millisecond)
		t.Logf("repro iter %d: immediate stop after quick restart", iter)
		quickStopAt := time.Now()
		quickStopCode := int(Stop())
		quickStopCost := time.Since(quickStopAt)
		if quickStopCost > stats.maxStopCost {
			stats.maxStopCost = quickStopCost
		}
		if quickStopCode != 0 {
			stats.stopNonZero.Add(1)
		}
		if quickStopCost >= 12*time.Second {
			stats.stopSlow.Add(1)
		}

		if c := atomic.LoadInt64(activeSSE); c > stats.maxSSEActiveObserved {
			stats.maxSSEActiveObserved = c
		}

		time.Sleep(time.Duration(15+rnd.Intn(80)) * time.Millisecond)
	}

	cancel()
	wg.Wait()
	_ = Stop()

	t.Logf("repro stats: iterations=%d stop_non_zero=%d stop_slow=%d start_non_zero=%d pseudo_start=%d http_timeout=%d hammer_timeout=%d sse_dial_errors=%d max_sse_active=%d max_start_cost=%v max_stop_cost=%v",
		stats.iterations,
		stats.stopNonZero.Load(),
		stats.stopSlow.Load(),
		stats.startNonZero.Load(),
		stats.pseudoStart.Load(),
		stats.httpTimeout.Load(),
		stats.hammerTimeout.Load(),
		stats.sseDialErrors.Load(),
		stats.maxSSEActiveObserved,
		stats.maxStartCost,
		stats.maxStopCost,
	)

	reproduced := stats.pseudoStart.Load() > 0 ||
		stats.httpTimeout.Load() > 0

	if reproduced {
		t.Fatalf("reproduction hit: pseudo_start=%d http_timeout=%d stop_non_zero=%d",
			stats.pseudoStart.Load(),
			stats.httpTimeout.Load(),
			stats.stopNonZero.Load(),
		)
	}

	if stats.stopNonZero.Load() > 0 {
		t.Logf("repro script observed stop!=0 but recovered without pseudo-start")
	}
}

func runRealInstanceCycles(t *testing.T, basePath string, opt cycleOptions) restartStats {
	t.Helper()

	if opt.rounds <= 0 {
		opt.rounds = 1
	}
	if opt.openWait <= 0 {
		opt.openWait = stateWaitOpen
	}
	if opt.closeWait <= 0 {
		opt.closeWait = stateWaitOff
	}

	stats := restartStats{rounds: opt.rounds}

	cfg := StartConfig{
		BasePath:    basePath,
		Host:        testHost,
		Port:        testPort,
		FrontendURL: frontendURL,
		OutputDir:   filepath.Join(basePath, "records"),
		SSEToken:    "test-token",
	}

	for i := 1; i <= opt.rounds; i++ {
		t.Logf("round %d: starting", i)
		startAt := time.Now()
		startCode := start(cfg)
		startCost := time.Since(startAt)
		if startCost > stats.maxStartCost {
			stats.maxStartCost = startCost
		}

		if startCode != 0 {
			stats.startFailed++
			t.Logf("round %d: start() returned %d", i, startCode)
			continue
		}

		if opt.doubleStart {
			doubleStartCode := start(cfg)
			if doubleStartCode != 0 {
				stats.startFailed++
				t.Logf("round %d: second start() returned %d", i, doubleStartCode)
			}
		}

		if err := waitForTCPState(testAddr, true, opt.openWait); err != nil {
			stats.pseudoStart++
			t.Logf("round %d: pseudo-start suspected, start=0 but %s is not listening: %v", i, testAddr, err)
		}

		timedOut, err := probeHTTPNoTimeout("http://localhost:8080", 1200*time.Millisecond)
		if timedOut {
			stats.httpTimeout++
			t.Logf("round %d: HTTP probe timeout on http://localhost:8080: %v", i, err)
		} else if err != nil {
			t.Logf("round %d: HTTP probe returned non-timeout error (ignored): %v", i, err)
		}

		t.Logf("round %d: started (start_cost=%v)", i, startCost)

		t.Logf("round %d: stopping", i)
		stopAt := time.Now()
		stopCode := Stop()
		stopCost := time.Since(stopAt)
		if stopCost > stats.maxStopCost {
			stats.maxStopCost = stopCost
		}

		if stopCode != 0 {
			stats.stopFailed++
			t.Logf("round %d: Stop() returned %d", i, stopCode)
		}

		if opt.doubleStop {
			doubleStopCode := Stop()
			if doubleStopCode != 0 {
				stats.stopFailed++
				t.Logf("round %d: second Stop() returned %d", i, doubleStopCode)
			}
		}

		if err := waitForTCPState(testAddr, false, opt.closeWait); err != nil {
			stats.closeFailed++
			t.Logf("round %d: app did not fully stop on %s: %v", i, testAddr, err)
		}

		t.Logf("round %d: stopped (stop_cost=%v)", i, stopCost)

		if opt.interRoundGap > 0 {
			time.Sleep(opt.interRoundGap)
		}
	}

	return stats
}

func ensurePort8080Available(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp", testAddr)
	if err != nil {
		t.Skipf("skip real-instance port test because %s is not available: %v", testAddr, err)
	}
	_ = ln.Close()
}

func waitForTCPState(addr string, wantOpen bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 150*time.Millisecond)
		isOpen := err == nil
		if conn != nil {
			_ = conn.Close()
		}

		if isOpen == wantOpen {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}

	state := "closed"
	if wantOpen {
		state = "open"
	}
	return fmt.Errorf("tcp endpoint did not become %s within %v", state, timeout)
}

func logRestartStats(t *testing.T, stats restartStats) {
	t.Helper()
	t.Logf("restart stats: rounds=%d start_failed=%d pseudo_start=%d http_timeout=%d stop_failed=%d port_close_failed=%d max_start_cost=%v max_stop_cost=%v",
		stats.rounds,
		stats.startFailed,
		stats.pseudoStart,
		stats.httpTimeout,
		stats.stopFailed,
		stats.closeFailed,
		stats.maxStartCost,
		stats.maxStopCost,
	)
}

func probeHTTPNoTimeout(url string, timeout time.Duration) (bool, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return true, err
		}
		if os.IsTimeout(err) {
			return true, err
		}
		return false, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return false, nil
}

// func skipIfCI(t *testing.T) {
// 	t.Helper()
// 	if os.Getenv("CI") != "" {
// 		t.Skip("skip real-instance androidlib restart tests on CI")
// 	}
// }

func runConcurrentCalls(workers int, fn func() int) []int {
	if workers <= 0 {
		workers = 1
	}

	results := make([]int, workers)
	startCh := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		idx := i
		go func() {
			defer wg.Done()
			<-startCh
			results[idx] = fn()
		}()
	}

	close(startCh)
	wg.Wait()
	return results
}

func spawnSSEPressure(ctx context.Context, wg *sync.WaitGroup, workers int, stats *reproScriptStats) *int64 {
	active := new(int64)
	client := &http.Client{}
	url := "http://localhost:8080/notify/sse?token=test-token"

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
				if err != nil {
					return
				}

				resp, err := client.Do(req)
				if err != nil {
					stats.sseDialErrors.Add(1)
					time.Sleep(40 * time.Millisecond)
					continue
				}

				atomic.AddInt64(active, 1)
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				atomic.AddInt64(active, -1)
				time.Sleep(20 * time.Millisecond)
			}
		}()
	}

	return active
}

func spawnHTTPHammer(ctx context.Context, wg *sync.WaitGroup, workers int, stats *reproScriptStats) {
	url := "http://localhost:8080"
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := &http.Client{Timeout: 900 * time.Millisecond}
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				resp, err := client.Get(url)
				if err != nil {
					var netErr net.Error
					if errors.As(err, &netErr) && netErr.Timeout() {
						stats.hammerTimeout.Add(1)
					}
					time.Sleep(35 * time.Millisecond)
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				time.Sleep(35 * time.Millisecond)
			}
		}()
	}
}
