package cloudconvert_test

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/cloudconvert"
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/sirupsen/logrus"
)

const (
	GenerateChunkSize = 128 * 1024 * 1024 // 128MB

	// File sizes for testing
	Size100MB = 100 * 1024 * 1024
	Size500MB = 500 * 1024 * 1024
	Size1GB   = 1024 * 1024 * 1024

	// Test file names
	TestFileTxt = "test_large_1gb.txt"
	TestFileZip = "test_large_1gb.zip"
	TestFileTar = "test_large_1gb.tar.gz"
)

func init() {
	logrus.SetLevel(logrus.DebugLevel)
	os.Setenv("DEBUG", "true")
}

// ============================================================================
// Helper Functions for File Generation
// ============================================================================

// generateRandomFile creates a file with random data of specified size
func generateRandomFile(path string, size int64) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer f.Close()

	// Generate in chunks to avoid memory issues
	const chunkSize = GenerateChunkSize
	buffer := make([]byte, chunkSize)
	remaining := size

	for remaining > 0 {
		toWrite := chunkSize
		if remaining < int64(chunkSize) {
			toWrite = int(remaining)
			buffer = buffer[:toWrite]
		}

		if _, err := rand.Read(buffer); err != nil {
			return fmt.Errorf("failed to generate random data:  %w", err)
		}

		if _, err := f.Write(buffer); err != nil {
			return fmt.Errorf("failed to write data:  %w", err)
		}

		remaining -= int64(toWrite)
	}

	return nil
}

// generateLargeTxtFile creates a large text file
func generateLargeTxtFile(path string, size int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Generate realistic text data
	const chunkSize = GenerateChunkSize // 100MB chunks
	line := "This is a test line with some realistic content to simulate large file uploads.  Line number: %d\n"
	remaining := size
	lineNum := 0

	for remaining > 0 {
		chunk := ""
		chunkLen := 0
		for chunkLen < chunkSize && remaining > 0 {
			lineStr := fmt.Sprintf(line, lineNum)
			chunk += lineStr
			chunkLen += len(lineStr)
			lineNum++
		}

		if _, err := f.WriteString(chunk); err != nil {
			return err
		}
		remaining -= int64(len(chunk))
	}

	return nil
}

// generateLargeZipFile creates a large zip file
func generateLargeZipFile(path string, size int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	zipWriter := zip.NewWriter(f)
	defer zipWriter.Close()

	// Create multiple files in the zip
	numFiles := 10
	sizePerFile := size / int64(numFiles)

	for i := 0; i < numFiles; i++ {
		fileName := fmt.Sprintf("file_%d.dat", i)
		fileWriter, err := zipWriter.Create(fileName)
		if err != nil {
			return err
		}

		// Write random data
		const chunkSize = GenerateChunkSize
		buffer := make([]byte, chunkSize)
		remaining := sizePerFile

		for remaining > 0 {
			toWrite := chunkSize
			if remaining < int64(chunkSize) {
				toWrite = int(remaining)
				buffer = buffer[:toWrite]
			}

			if _, err := rand.Read(buffer); err != nil {
				return err
			}

			if _, err := fileWriter.Write(buffer); err != nil {
				return err
			}

			remaining -= int64(toWrite)
		}
	}

	return nil
}

// generateLargeTarGzFile creates a large tar.gz file
func generateLargeTarGzFile(path string, size int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	gzWriter := gzip.NewWriter(f)
	defer gzWriter.Close()

	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	// Create multiple files in the tar
	numFiles := 10
	sizePerFile := size / int64(numFiles)

	for i := 0; i < numFiles; i++ {
		fileName := fmt.Sprintf("file_%d.dat", i)

		// Write tar header
		header := &tar.Header{
			Name: fileName,
			Mode: 0600,
			Size: sizePerFile,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}

		// Write random data
		const chunkSize = GenerateChunkSize
		buffer := make([]byte, chunkSize)
		remaining := sizePerFile

		for remaining > 0 {
			toWrite := chunkSize
			if remaining < int64(chunkSize) {
				toWrite = int(remaining)
				buffer = buffer[:toWrite]
			}

			if _, err := rand.Read(buffer); err != nil {
				return err
			}

			if _, err := tarWriter.Write(buffer); err != nil {
				return err
			}

			remaining -= int64(toWrite)
		}
	}

	return nil
}

// ============================================================================
// Memory Tracking Utilities
// ============================================================================

type MemStats struct {
	AllocMB      float64
	TotalAllocMB float64
	SysMB        float64
	NumGC        uint32
}

func getMemStats() MemStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return MemStats{
		AllocMB:      float64(m.Alloc) / 1024 / 1024,
		TotalAllocMB: float64(m.TotalAlloc) / 1024 / 1024,
		SysMB:        float64(m.Sys) / 1024 / 1024,
		NumGC:        m.NumGC,
	}
}

func logMemStats(t testing.TB, label string) MemStats {
	stats := getMemStats()
	t.Logf("[%s] Memory - Alloc: %.2f MB, TotalAlloc: %.2f MB, Sys: %.2f MB, NumGC:  %d",
		label, stats.AllocMB, stats.TotalAllocMB, stats.SysMB, stats.NumGC)
	return stats
}

// ============================================================================
// Setup/Teardown
// ============================================================================

func setupTestFile(t *testing.T, filename string, size int64) string {
	t.Helper()

	if _, err := os.Stat(filename); err == nil {
		fileInfo, _ := os.Stat(filename)
		if fileInfo.Size() >= size {
			t.Logf("✅ Test file %s already exists with correct size, skipping generation", filename)
			return filename
		}
		t.Logf("⚠️  Test file %s exists but wrong size, regenerating.. .", filename)
		os.Remove(filename)
	}

	t.Logf("📝 Generating test file: %s (%.2f MB)...", filename, float64(size)/1024/1024)
	start := time.Now()

	var err error
	ext := filepath.Ext(filename)
	switch ext {
	case ".txt":
		err = generateLargeTxtFile(filename, size)
	case ".zip":
		err = generateLargeZipFile(filename, size)
	case ".gz":
		err = generateLargeTarGzFile(filename, size)
	default:
		err = generateRandomFile(filename, size)
	}

	if err != nil {
		t.Fatalf("Failed to generate test file: %v", err)
	}

	t.Logf("✅ Generated test file in %v", time.Since(start))
	return filename
}

func cleanupTestFile(t *testing.T, filename string) {
	if os.Getenv("KEEP_TEST_FILES") == "true" {
		t.Logf("🔒 Keeping test file: %s", filename)
		return
	}
	if err := os.Remove(filename); err != nil && !os.IsNotExist(err) {
		t.Logf("⚠️  Failed to cleanup test file %s: %v", filename, err)
	} else {
		t.Logf("🧹 Cleaned up test file: %s", filename)
	}
}

// ============================================================================
// Memory Tests for Upload
// ============================================================================

// TestMemoryUsage_Upload100MB tests memory usage with 100MB file upload
func TestMemoryUsage_Upload100MB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}

	testMemoryUpload(t, "test_100mb.zip", Size100MB)
}

// TestMemoryUsage_Upload500MB tests memory usage with 500MB file upload
func TestMemoryUsage_Upload500MB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}

	testMemoryUpload(t, "test_500mb.zip", Size500MB)
}

// TestMemoryUsage_Upload1GB tests memory usage with 1GB file upload (TXT)
func TestMemoryUsage_Upload1GB_TXT(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}

	testMemoryUpload(t, TestFileTxt, Size1GB)
}

// TestMemoryUsage_Upload1GB_ZIP tests memory usage with 1GB file upload (ZIP)
func TestMemoryUsage_Upload1GB_ZIP(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}

	testMemoryUpload(t, TestFileZip, Size1GB)
}

// TestMemoryUsage_Upload1GB_TAR tests memory usage with 1GB file upload (TAR.GZ)
func TestMemoryUsage_Upload1GB_TAR(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}

	testMemoryUpload(t, TestFileTar, Size1GB)
}

func testMemoryUpload(t *testing.T, filename string, size int64) {
	t.Logf("=== Starting Memory Upload Test for %s ===", filename)

	// Setup
	filepath := setupTestFile(t, filename, size)
	defer cleanupTestFile(t, filepath)

	// Force GC before test
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	beforeStats := logMemStats(t, "BEFORE UPLOAD")

	// Create client
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	client := cloudconvert.NewClient(ctx, os.Getenv("CLOUDCONVERT_API_KEY"))

	// Open file
	f, err := os.Open(filepath)
	if err != nil {
		t.Fatal(err)
	}

	afterOpenStats := logMemStats(t, "AFTER FILE OPEN")

	// Create upload task
	const (
		importTaskName = "import-source-memory"
		exportTaskName = "export-output-memory"
	)

	job, err := client.NewJobBuilder().
		AddTask(cloudconvert.NewImportUploadTask(importTaskName, &cloudconvert.ImportUploadRequest{})).
		AddTask(cloudconvert.NewExportURLTask(exportTaskName, &cloudconvert.ExportURLRequest{
			Input: importTaskName,
		})).
		Submit()
	if err != nil {
		t.Fatal(err)
	}

	uploadTask := job.TaskData(importTaskName)
	if uploadTask == nil {
		t.Fatalf("import task data not found for task name %s", importTaskName)
	}

	exportTaskID := job.TaskID(exportTaskName)
	if exportTaskID == "" {
		t.Fatalf("export task id not found for task name %s", exportTaskName)
	}

	t.Logf("✅ Created upload task:  %s", uploadTask.ID)
	t.Logf("✅ Created export URL task: %s", exportTaskID)

	afterTaskStats := logMemStats(t, "AFTER CREATE TASK")

	// Upload file - THIS IS WHERE THE MEMORY SPIKE HAPPENS
	t.Logf("📤 Starting upload...")
	uploadStart := time.Now()

	if err := client.UploadFileToTask(f, uploadTask.Result.Form); err != nil {
		t.Fatalf("Upload failed: %v", err)
	}

	uploadDuration := time.Since(uploadStart)
	t.Logf("✅ Upload completed in %v", uploadDuration)

	afterUploadStats := logMemStats(t, "AFTER UPLOAD")

	if os.Getenv("CLOUDCONVERT_TASK_ID") == "" {
		t.Logf("✅ Use this task ID for download test: %s", exportTaskID)
	}

	// Force GC to see retained memory
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	afterGCStats := logMemStats(t, "AFTER GC")

	// Calculate memory increase
	memIncreaseDuringUpload := afterUploadStats.AllocMB - beforeStats.AllocMB
	peakMemory := afterUploadStats.AllocMB
	fileSizeMB := float64(size) / 1024 / 1024
	memoryRatio := memIncreaseDuringUpload / fileSizeMB

	// Report findings
	t.Logf("\n"+
		"==================== MEMORY TEST RESULTS ====================\n"+
		"File: %s\n"+
		"File Size: %.2f MB\n"+
		"Upload Duration: %v\n"+
		"-----------------------------------------------------------\n"+
		"Memory Before Upload: %.2f MB\n"+
		"Memory After File Open: %.2f MB (increase: %.2f MB)\n"+
		"Memory After Create Task:  %.2f MB (increase: %.2f MB)\n"+
		"Peak Memory (After Upload): %.2f MB (increase: %.2f MB)\n"+
		"Memory After GC: %.2f MB (retained: %.2f MB)\n"+
		"-----------------------------------------------------------\n"+
		"Memory Increase / File Size Ratio: %.2fx\n"+
		"GC Count Increase: %d\n"+
		"===========================================================\n",
		filename,
		fileSizeMB,
		uploadDuration,
		beforeStats.AllocMB,
		afterOpenStats.AllocMB, afterOpenStats.AllocMB-beforeStats.AllocMB,
		afterTaskStats.AllocMB, afterTaskStats.AllocMB-afterOpenStats.AllocMB,
		peakMemory, memIncreaseDuringUpload,
		afterGCStats.AllocMB, afterGCStats.AllocMB-beforeStats.AllocMB,
		memoryRatio,
		afterUploadStats.NumGC-beforeStats.NumGC,
	)

	// Check for memory issues
	if memoryRatio > 1.5 {
		t.Errorf("❌ MEMORY ISSUE DETECTED: Memory usage is %.2fx the file size (expected < 1.5x)", memoryRatio)
		t.Error("This indicates the entire file is being buffered in memory!")
	} else if memoryRatio > 0.5 {
		t.Logf("⚠️  WARNING: Memory usage is %.2fx the file size (expected < 0.5x for streaming)", memoryRatio)
	} else {
		t.Logf("✅ Memory usage is acceptable:  %.2fx the file size", memoryRatio)
	}
}

// ============================================================================
// Memory Tests for Download
// ============================================================================

// TestMemoryUsage_Download tests memory usage during file download
func TestMemoryUsage_Download(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}
	if os.Getenv("CLOUDCONVERT_TASK_ID") == "" {
		t.Skip("CLOUDCONVERT_TASK_ID not set, skipping test - run upload test first")
	}

	t.Logf("=== Starting Memory Download Test ===")

	// Force GC before test
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	beforeStats := logMemStats(t, "BEFORE DOWNLOAD")

	// Create client
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	client := cloudconvert.NewClient(ctx, os.Getenv("CLOUDCONVERT_API_KEY"))

	// Get task details
	task, err := client.GetTask(os.Getenv("CLOUDCONVERT_TASK_ID"))
	if err != nil {
		t.Fatal(err)
	}

	afterTaskStats := logMemStats(t, "AFTER GET TASK")

	// Check task status
	t.Logf("Task status: %v", task.Data.Status)
	switch task.Data.Status {
	case cloudconvert.TaskStatusWaiting, cloudconvert.TaskStatusProcessing:
		t.Skip("Task is not finished yet, skipping download test")
	case cloudconvert.TaskStatusError:
		t.Fatal("Task ended with error status")
	}

	if len(task.Data.Result.Files) == 0 {
		t.Fatal("No files found in task result")
	}

	downloadURL := task.Data.Result.Files[0].URL
	filename := task.Data.Result.Files[0].Filename
	fileSize := task.Data.Result.Files[0].Size

	t.Logf("Download URL: %v", downloadURL)
	t.Logf("File size: %.2f MB", float64(fileSize)/1024/1024)

	// Start download
	t.Logf("📥 Starting download...")
	downloadStart := time.Now()

	stream, err := client.DownloadAsFileStream(downloadURL)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	afterStreamStats := logMemStats(t, "AFTER GET STREAM")

	// Write to file
	outputPath := "downloaded_" + filename
	defer cleanupTestFile(t, outputPath)

	outFile, err := os.Create(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer outFile.Close()

	t.Logf("downloading to file: %v", outputPath)
	totalWritten, err := outFile.ReadFrom(stream)
	if err != nil {
		t.Fatal(err)
	}

	downloadDuration := time.Since(downloadStart)
	t.Logf("✅ Download completed in %v", downloadDuration)
	t.Logf("Total downloaded: %.2f MB", float64(totalWritten)/1024/1024)

	afterDownloadStats := logMemStats(t, "AFTER DOWNLOAD")

	// Force GC
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	afterGCStats := logMemStats(t, "AFTER GC")

	// Calculate memory increase
	memIncreaseDuringDownload := afterDownloadStats.AllocMB - beforeStats.AllocMB
	peakMemory := afterDownloadStats.AllocMB
	fileSizeMB := float64(fileSize) / 1024 / 1024
	memoryRatio := memIncreaseDuringDownload / fileSizeMB

	// Report findings
	t.Logf("\n"+
		"==================== DOWNLOAD TEST RESULTS ====================\n"+
		"File: %s\n"+
		"File Size:  %.2f MB\n"+
		"Download Duration: %v\n"+
		"Download Speed: %.2f MB/s\n"+
		"-------------------------------------------------------------\n"+
		"Memory Before Download: %.2f MB\n"+
		"Memory After Get Task: %.2f MB (increase: %.2f MB)\n"+
		"Memory After Get Stream: %.2f MB (increase: %.2f MB)\n"+
		"Peak Memory (After Download): %.2f MB (increase: %.2f MB)\n"+
		"Memory After GC: %.2f MB (retained: %.2f MB)\n"+
		"-------------------------------------------------------------\n"+
		"Memory Increase / File Size Ratio: %.2fx\n"+
		"GC Count Increase: %d\n"+
		"===============================================================\n",
		filename,
		fileSizeMB,
		downloadDuration,
		fileSizeMB/downloadDuration.Seconds(),
		beforeStats.AllocMB,
		afterTaskStats.AllocMB, afterTaskStats.AllocMB-beforeStats.AllocMB,
		afterStreamStats.AllocMB, afterStreamStats.AllocMB-afterTaskStats.AllocMB,
		peakMemory, memIncreaseDuringDownload,
		afterGCStats.AllocMB, afterGCStats.AllocMB-beforeStats.AllocMB,
		memoryRatio,
		afterDownloadStats.NumGC-beforeStats.NumGC,
	)

	// Check for memory issues
	if memoryRatio > 1.5 {
		t.Errorf("❌ MEMORY ISSUE DETECTED: Memory usage is %.2fx the file size (expected < 1.5x)", memoryRatio)
		t.Error("This indicates the entire file is being buffered in memory!")
	} else if memoryRatio > 0.5 {
		t.Logf("⚠️  WARNING: Memory usage is %.2fx the file size (expected < 0.5x for streaming)", memoryRatio)
	} else {
		t.Logf("✅ Memory usage is acceptable:  %.2fx the file size", memoryRatio)
	}
}

// TestMemoryUsage_DownloadWithStreamToFile tests download using utils.StreamToFile (like service)
func TestMemoryUsage_DownloadWithStreamToFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}
	if os.Getenv("CLOUDCONVERT_TASK_ID") == "" {
		t.Skip("CLOUDCONVERT_TASK_ID not set, skipping test - run upload test first")
	}

	t.Logf("=== Starting Memory Download Test (StreamToFile like service) ===")

	// Force GC before test
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	beforeStats := logMemStats(t, "BEFORE DOWNLOAD")

	// Create client
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	client := cloudconvert.NewClient(ctx, os.Getenv("CLOUDCONVERT_API_KEY"))

	// Get task details
	task, err := client.GetTask(os.Getenv("CLOUDCONVERT_TASK_ID"))
	if err != nil {
		t.Fatal(err)
	}

	afterTaskStats := logMemStats(t, "AFTER GET TASK")

	// Check task status
	t.Logf("Task status: %v", task.Data.Status)
	switch task.Data.Status {
	case cloudconvert.TaskStatusWaiting, cloudconvert.TaskStatusProcessing:
		t.Skip("Task is not finished yet, skipping download test")
	case cloudconvert.TaskStatusError:
		t.Fatal("Task ended with error status")
	}

	if len(task.Data.Result.Files) == 0 {
		t.Fatal("No files found in task result")
	}

	downloadURL := task.Data.Result.Files[0].URL
	filename := task.Data.Result.Files[0].Filename
	fileSize := task.Data.Result.Files[0].Size

	t.Logf("Download URL:  %v", downloadURL)
	t.Logf("File size: %.2f MB", float64(fileSize)/1024/1024)

	// Start download - get stream
	t.Logf("📥 Starting download with StreamToFile...")
	downloadStart := time.Now()

	stream, err := client.DownloadAsFileStream(downloadURL)
	if err != nil {
		t.Fatal(err)
	}

	afterStreamStats := logMemStats(t, "AFTER GET STREAM")

	// Use StreamToFile like the service does
	outputPath := "downloaded_streamtofile_" + filename
	defer cleanupTestFile(t, outputPath)

	t.Logf("downloading to file: %v", outputPath)

	writer := pool.NewFileStreamWriter(t.Context(), pool.NewBytesPool(5*1024*1024))

	if err := writer.WriteToFile(stream, outputPath, 1*1024*1024); err != nil {
		t.Fatalf("StreamToFile failed: %v", err)
	}

	downloadDuration := time.Since(downloadStart)
	t.Logf("✅ Download completed in %v", downloadDuration)

	// Verify file size
	fileInfo, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("Failed to stat downloaded file: %v", err)
	}

	totalWritten := fileInfo.Size()
	t.Logf("Total downloaded: %.2f MB", float64(totalWritten)/1024/1024)

	afterDownloadStats := logMemStats(t, "AFTER DOWNLOAD")

	// Force GC
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	afterGCStats := logMemStats(t, "AFTER GC")

	// Calculate memory increase
	memIncreaseDuringDownload := afterDownloadStats.AllocMB - beforeStats.AllocMB
	peakMemory := afterDownloadStats.AllocMB
	fileSizeMB := float64(fileSize) / 1024 / 1024
	memoryRatio := memIncreaseDuringDownload / fileSizeMB

	// Report findings
	t.Logf("\n"+
		"==================== DOWNLOAD TEST RESULTS (StreamToFile) ====================\n"+
		"File: %s\n"+
		"File Size: %.2f MB\n"+
		"Download Duration: %v\n"+
		"Download Speed: %.2f MB/s\n"+
		"-----------------------------------------------------------------------------\n"+
		"Memory Before Download: %.2f MB\n"+
		"Memory After Get Task: %.2f MB (increase: %.2f MB)\n"+
		"Memory After Get Stream: %.2f MB (increase: %.2f MB)\n"+
		"Peak Memory (After Download): %.2f MB (increase: %.2f MB)\n"+
		"Memory After GC: %.2f MB (retained: %.2f MB)\n"+
		"-----------------------------------------------------------------------------\n"+
		"Memory Increase / File Size Ratio: %.2fx\n"+
		"GC Count Increase: %d\n"+
		"Buffer Pool Size: 5 MB (from downloadPool)\n"+
		"=============================================================================\n",
		filename,
		fileSizeMB,
		downloadDuration,
		fileSizeMB/downloadDuration.Seconds(),
		beforeStats.AllocMB,
		afterTaskStats.AllocMB, afterTaskStats.AllocMB-beforeStats.AllocMB,
		afterStreamStats.AllocMB, afterStreamStats.AllocMB-afterTaskStats.AllocMB,
		peakMemory, memIncreaseDuringDownload,
		afterGCStats.AllocMB, afterGCStats.AllocMB-beforeStats.AllocMB,
		memoryRatio,
		afterDownloadStats.NumGC-beforeStats.NumGC,
	)

	// Check for memory issues
	if memoryRatio > 1.5 {
		t.Errorf("❌ MEMORY ISSUE DETECTED:  Memory usage is %.2fx the file size (expected < 1.5x)", memoryRatio)
		t.Error("This indicates the entire file is being buffered in memory!")
	} else if memoryRatio > 0.5 {
		t.Logf("⚠️  WARNING:  Memory usage is %.2fx the file size (expected < 0.5x for streaming)", memoryRatio)
	} else {
		t.Logf("✅ Memory usage is acceptable: %.2fx the file size", memoryRatio)
	}
}
