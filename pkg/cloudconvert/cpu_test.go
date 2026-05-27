package cloudconvert_test

import (
	"context"
	"os"
	"os/exec"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/cloudconvert"
	"github.com/bilirec/bilirec/pkg/pool"
)

// startCPUProfile starts a CPU profile and returns the profile file path and a stop func
func startCPUProfile(t testing.TB, name string) (string, func()) {
	t.Helper()
	f, err := os.CreateTemp("", name+"-*.pprof")
	if err != nil {
		t.Fatalf("failed to create temp profile file: %v", err)
	}

	if err := pprof.StartCPUProfile(f); err != nil {
		f.Close()
		t.Fatalf("failed to start cpu profile: %v", err)
	}

	stop := func() {
		pprof.StopCPUProfile()
		f.Close()
		// Try to run `go tool pprof -top` and include a concise summary in the test log.
		if out, err := exec.Command("go", "tool", "pprof", "-nodecount=20", "-top", f.Name()).CombinedOutput(); err != nil {
			t.Logf("CPU profile written to %s (pprof -top failed: %v)", f.Name(), err)
			if len(out) > 0 {
				t.Logf("pprof output:\n%s", string(out))
			}
		} else {
			t.Logf("CPU profile written to %s\npprof -top:\n%s", f.Name(), string(out))
		}
	}

	return f.Name(), stop
}

func TestCPUUsage_Upload1GB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}

	// prepare file
	filePath := setupTestFile(t, "cpu_test_1gb.zip", Size1GB)
	defer cleanupTestFile(t, filePath)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	client := cloudconvert.NewClient(ctx, os.Getenv("CLOUDCONVERT_API_KEY"))
	const (
		importTaskName = "import-source-cpu"
		exportTaskName = "export-output-cpu"
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
	t.Logf("Created upload task: %s", uploadTask.ID)
	t.Logf("Export task id=%s (use CLOUDCONVERT_TASK_ID to download)", exportTaskID)

	// open file
	f, err := os.Open(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// start CPU profile
	profilePath, stop := startCPUProfile(t, "upload_cpu")
	defer func() {
		stop()
		// keep profile if KEEP_TEST_FILES is true
		if os.Getenv("KEEP_TEST_FILES") == "true" {
			t.Logf("kept profile: %s", profilePath)
		}
	}()

	start := time.Now()
	if err := client.UploadFileToTask(f, uploadTask.Result.Form); err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	dur := time.Since(start)
	t.Logf("Upload completed in %v", dur)
}

func TestCPUUsage_Download(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}
	if os.Getenv("CLOUDCONVERT_TASK_ID") == "" {
		t.Skip("CLOUDCONVERT_TASK_ID not set, skipping test - run upload test first")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	client := cloudconvert.NewClient(ctx, os.Getenv("CLOUDCONVERT_API_KEY"))
	task, err := client.GetTask(os.Getenv("CLOUDCONVERT_TASK_ID"))
	if err != nil {
		t.Fatal(err)
	}

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

	// start CPU profile
	profilePath, stop := startCPUProfile(t, "download_cpu")
	defer func() {
		stop()
		if os.Getenv("KEEP_TEST_FILES") == "true" {
			t.Logf("kept profile: %s", profilePath)
		}
	}()

	start := time.Now()
	stream, err := client.DownloadAsFileStream(downloadURL)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	defer os.Remove("tmp/" + filename)

	writer := pool.NewFileStreamWriter(t.Context(), pool.NewBytesPool(5*1024*1024))

	if err := writer.WriteToFile(stream, "/tmp/"+filename, 1*1024*1024); err != nil {
		t.Fatal(err)
	}

	dur := time.Since(start)
	t.Logf("Download %s completed in %v", filename, dur)
}
