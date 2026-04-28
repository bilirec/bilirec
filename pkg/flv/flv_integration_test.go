package flv_test

import (
	"io"
	"os"
	"testing"

	"github.com/eric2788/bilirec/pkg/flv"
)

func TestRealtimeFixer(t *testing.T) {
	// ============================================
	// Example 1: Realtime Fixer
	// ============================================
	realtimeFixer := flv.NewRealtimeFixer()
	defer realtimeFixer.Close()

	inputChannel := make(chan []byte, 100)
	done := make(chan bool)

	if _, err := os.Stat("original.flv"); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("skipping realtime fixer integration test: fixture %q not found", "original.flv")
		}
		t.Fatalf("Failed to stat input fixture: %v", err)
	}

	outputFile, err := os.Create("output_realtime.flv")
	if err != nil {
		t.Fatalf("Failed to create output file: %v", err)
	}
	defer outputFile.Close()

	var totalRead int64
	var totalWritten int64
	var chunkCount int

	go func() {
		defer close(done)

		for chunk := range inputChannel {
			chunkCount++
			totalRead += int64(len(chunk))

			fixed, err := realtimeFixer.Fix(chunk)
			if err != nil {
				t.Errorf("❌ Fix error on chunk #%d: %v", chunkCount, err)
				continue
			}

			if len(fixed) > 0 {
				written, err := outputFile.Write(fixed)
				if err != nil {
					t.Errorf("❌ Write error:  %v", err)
					continue
				}
				totalWritten += int64(written)

				// 每 100 個 chunk 輸出進度
				if chunkCount%100 == 0 {
					t.Logf("📊 Progress: %d chunks, %d KB read, %d KB written",
						chunkCount, totalRead/1024, totalWritten/1024)
				}
			}
		}

		t.Logf("✅ Processing complete: %d chunks processed", chunkCount)
	}()

	// Feed data from source
	t.Log("📥 Starting to read original. flv...")
	sendFile(t, "original.flv", inputChannel)

	// Wait for processing to complete
	<-done

	// Validate output
	stat, err := outputFile.Stat()
	if err != nil {
		t.Fatalf("Failed to stat output file: %v", err)
	}

	if stat.Size() == 0 {
		t.Error("❌ Output file is empty!")
	} else {
		t.Logf("✅ Realtime fix complete:  %d bytes read, %d bytes written",
			totalRead, totalWritten)
		t.Logf("📁 Output file size: %d bytes (%.2f MB)",
			stat.Size(), float64(stat.Size())/(1024*1024))

		// 計算壓縮率/膨脹率
		ratio := float64(totalWritten) / float64(totalRead) * 100
		t.Logf("📈 Size ratio: %.2f%% (output/input)", ratio)
	}

	dups, size, capacity := realtimeFixer.GetDedupStats()
	t.Logf("🗂️ Dedup Stats: %d duplicates detected, cache size: %d/%d", dups, size, capacity)
}

func TestAccumulateFix(t *testing.T) {
	// ============================================
	// Example 2: Accumulate Fixer (每 10MB 輸出)
	// ============================================
	accFixer := flv.NewAccumulateFixer(10) // 10 MB chunks
	defer accFixer.Close()

	if _, err := os.Stat("original.flv"); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("skipping accumulate fixer integration test: fixture %q not found", "original.flv")
		}
		t.Fatalf("Failed to stat input fixture: %v", err)
	}

	outputFile2, err := os.Create("output_accumulated.flv")
	if err != nil {
		t.Fatalf("Failed to create output file: %v", err)
	}
	defer outputFile2.Close()

	inputChannel2 := make(chan []byte, 100)
	done := make(chan bool)

	var totalWritten int64
	var flushCount int

	go func() {
		defer close(done)

		for chunk := range inputChannel2 {
			shouldFlush, err := accFixer.Accumulate(chunk)
			if err != nil {
				t.Errorf("Accumulate error: %v", err)
				continue
			}

			if shouldFlush {
				buffered, processed := accFixer.GetStats()
				t.Logf("📊 Stats before flush: buffered=%d, processed=%d", buffered, processed)

				fixed, err := accFixer.Flush()
				if err != nil {
					t.Errorf("Flush error: %v", err)
					continue
				}

				if len(fixed) > 0 {
					written, err := outputFile2.Write(fixed)
					if err != nil {
						t.Errorf("Write error: %v", err)
						continue
					}
					totalWritten += int64(written)
					flushCount++
					t.Logf("✅ Flush #%d: wrote %d bytes", flushCount, written)
				}
			}
		}

		// 🔥 重要: 使用 FlushRemaining() 而不是 Flush()
		t.Log("📦 Flushing remaining data...")
		final, err := accFixer.FlushRemaining()
		if err != nil {
			t.Errorf("⚠️ Final flush error: %v", err)
		} else if len(final) > 0 {
			written, err := outputFile2.Write(final)
			if err != nil {
				t.Errorf("Final write error: %v", err)
			} else {
				totalWritten += int64(written)
				flushCount++
				t.Logf("✅ Final flush:  wrote %d bytes", written)
			}
		}

		t.Logf("✅ Total written: %d bytes in %d flushes", totalWritten, flushCount)
	}()

	// 發送文件數據
	sendFile(t, "original.flv", inputChannel2)

	// 等待處理完成
	<-done

	// 驗證輸出
	stat, err := outputFile2.Stat()
	if err != nil {
		t.Fatalf("Failed to stat output file: %v", err)
	}

	if stat.Size() == 0 {
		t.Error("❌ Output file is empty!")
	} else {
		t.Logf("✅ Output file size: %d bytes", stat.Size())
	}

	dups, size, capacity := accFixer.GetDedupStats()
	t.Logf("🗂️ Dedup Stats: %d duplicates detected, cache size: %d/%d", dups, size, capacity)
}

func sendFile(t *testing.T, file string, ch chan<- []byte) {
	f, err := os.Open(file)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("skipping test for missing fixture: %q", file)
			return
		}
		t.Fatalf("Failed to open %q: %v", file, err)
		return
	}
	defer f.Close()

	defer close(ch)
	for {
		buf := make([]byte, 4096)
		n, err := f.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			ch <- buf[:n]
		}
	}
}
