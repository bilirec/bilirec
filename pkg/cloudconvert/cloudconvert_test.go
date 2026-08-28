package cloudconvert_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"

	"github.com/bilirec/bilirec/pkg/cloudconvert"
	"github.com/bilirec/bilirec/utils"
)

const (
	importTaskName  = "import-source"
	commandTaskName = "command-faststart"
	convertTaskName = "convert-output"
	exportTaskName  = "export-output"
)

func TestMain(m *testing.M) {
	utils.LoadDotEnvLocal()
	logger.SetLevel(logger.DebugLevel)
	os.Setenv("DEBUG", "true")
	os.Exit(m.Run())
}

func TestUploadFile(t *testing.T) {
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}

	const sourcePath = "test.flv"

	client := cloudconvert.NewClient(t.Context(), os.Getenv("CLOUDCONVERT_API_KEY"))
	f, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	job, err := client.NewJobBuilder().
		AddTask(cloudconvert.NewImportUploadTask(importTaskName, &cloudconvert.ImportUploadRequest{})).
		AddTask(cloudconvert.NewExportURLTask(exportTaskName, &cloudconvert.ExportURLRequest{
			Input: importTaskName,
		})).
		Submit()
	if err != nil {
		t.Fatal(err)
	}

	exportTaskID := job.TaskID(exportTaskName)
	if exportTaskID == "" {
		t.Fatalf("export task id not found for task name %s", exportTaskName)
	}
	uploadTask := job.TaskData(importTaskName)

	if err := client.UploadFileToTask(f, uploadTask.Result.Form); err != nil {
		t.Fatal(err)
	}

	t.Logf("Upload successful, import task id=%s", uploadTask.ID)
	t.Logf("Export task id=%s (use CLOUDCONVERT_TASK_ID to download)", exportTaskID)
}

func TestVideoConvertTask(t *testing.T) {
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}

	const sourcePath = "test.flv"

	client := cloudconvert.NewClient(t.Context(), os.Getenv("CLOUDCONVERT_API_KEY"))
	f, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	inputFormat := filepath.Ext(sourcePath)
	if inputFormat != "" {
		inputFormat = inputFormat[1:]
	}

	job, err := client.NewJobBuilder().
		AddTask(cloudconvert.NewImportUploadTask(importTaskName, &cloudconvert.ImportUploadRequest{})).
		AddTask(cloudconvert.NewVideoConvertTask(convertTaskName, &cloudconvert.VideoConvertPayload{
			Input:        importTaskName,
			InputFormat:  inputFormat,
			OutputFormat: "mp4",
			VideoCodec:   "copy",
			AudioCodec:   "copy",
			Filename:     "output.mp4",
		})).
		AddTask(cloudconvert.NewExportURLTask(exportTaskName, &cloudconvert.ExportURLRequest{
			Input: convertTaskName,
		})).
		Submit()
	if err != nil {
		t.Fatal(err)
	}

	convertTaskID := job.TaskID(convertTaskName)
	exportTaskID := job.TaskID(exportTaskName)
	if convertTaskID == "" {
		t.Fatalf("convert task id not found for task name %s", convertTaskName)
	}
	if exportTaskID == "" {
		t.Fatalf("export task id not found for task name %s", exportTaskName)
	}

	importTask := job.TaskData(importTaskName)
	if importTask == nil {
		t.Fatalf("import task data not found for task name %s", importTaskName)
	}

	if err := client.UploadFileToTask(f, importTask.Result.Form); err != nil {
		t.Fatal(err)
	}

	t.Logf("Job Response: %v", utils.PrettyPrintJSON(job.Job))
	t.Logf("Convert Task ID: %v", convertTaskID)
	t.Logf("Export Task ID: %v", exportTaskID)
}

func TestListTasks(t *testing.T) {
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}
	client := cloudconvert.NewClient(t.Context(), os.Getenv("CLOUDCONVERT_API_KEY"))

	tasks, err := client.ListTasks(&cloudconvert.TaskListFilter{
		JobID: "6ef54ed4-4267-4800-b24c-fe2d036d9bd3",
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Tasks: %v", utils.PrettyPrintJSON(tasks))
}

func TestGetJobDetails(t *testing.T) {
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}
	jobID := os.Getenv("CLOUDCONVERT_JOB_ID")
	if jobID == "" {
		t.Skip("CLOUDCONVERT_JOB_ID not set, skipping test")
	}

	client := cloudconvert.NewClient(t.Context(), os.Getenv("CLOUDCONVERT_API_KEY"))
	job, err := client.GetJobDetails(jobID)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Job Details: %v", utils.PrettyPrintJSON(job))
}

func TestGetTaskDetails(t *testing.T) {
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}
	taskID := os.Getenv("CLOUDCONVERT_TASK_ID")
	if taskID == "" {
		t.Skip("CLOUDCONVERT_TASK_ID not set, skipping test")
	}

	client := cloudconvert.NewClient(t.Context(), os.Getenv("CLOUDCONVERT_API_KEY"))

	task, err := client.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Task Details: %v", utils.PrettyPrintJSON(task))
}

func TestDownloadExportTask(t *testing.T) {
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}
	taskID := os.Getenv("CLOUDCONVERT_TASK_ID")
	if taskID == "" {
		t.Skip("CLOUDCONVERT_TASK_ID not set, skipping test")
	}

	client := cloudconvert.NewClient(t.Context(), os.Getenv("CLOUDCONVERT_API_KEY"))

	task, err := client.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}

	if task.Data.Status != cloudconvert.TaskStatusFinished {
		t.Fatalf("task %s is not finished (status: %s)", taskID, task.Data.Status)
	}
	if len(task.Data.Result.Files) == 0 || task.Data.Result.Files[0].URL == "" {
		t.Fatalf("task %s has no downloadable files", taskID)
	}

	t.Logf("Download URL: %v", task.Data.Result.Files[0].URL)
}

func TestRecoverImportedSourceFromTaskID(t *testing.T) {
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}
	importTaskID := os.Getenv("CLOUDCONVERT_IMPORT_TASK_ID")
	if importTaskID == "" {
		importTaskID = os.Getenv("CLOUDCONVERT_TASK_ID") // backward compatibility
	}
	if importTaskID == "" {
		t.Skip("CLOUDCONVERT_IMPORT_TASK_ID not set, skipping test")
	}

	client := cloudconvert.NewClient(t.Context(), os.Getenv("CLOUDCONVERT_API_KEY"))

	exportTask, err := client.CreateExportURL(&cloudconvert.ExportURLRequest{Input: importTaskID})
	if err != nil {
		t.Fatalf("create export task for import task %s failed: %v", importTaskID, err)
	}

	exportTaskID := exportTask.Data.ID
	if exportTaskID == "" {
		t.Fatalf("empty export task id for import task %s", importTaskID)
	}

	t.Logf("created export task id=%s from import task id=%s", exportTaskID, importTaskID)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	timeout := time.NewTimer(5 * time.Minute)
	defer timeout.Stop()

	for {
		select {
		case <-t.Context().Done():
			t.Skip("test context cancelled")
		case <-timeout.C:
			t.Fatalf("timeout waiting export task %s", exportTaskID)
		case <-ticker.C:
			task, err := client.GetTask(exportTaskID)
			if err != nil {
				t.Fatalf("get export task %s failed: %v", exportTaskID, err)
			}

			switch task.Data.Status {
			case cloudconvert.TaskStatusFinished:
				if len(task.Data.Result.Files) == 0 || task.Data.Result.Files[0].URL == "" {
					t.Fatalf("export task %s finished but no downloadable file url", exportTaskID)
				}
				t.Logf("Recovered source file URL: %s", task.Data.Result.Files[0].URL)
				t.Logf("Recovered source file name: %s", task.Data.Result.Files[0].Filename)
				t.Logf("Recovered source file size: %d", task.Data.Result.Files[0].Size)
				return
			case cloudconvert.TaskStatusError:
				msg := ""
				if task.Data.Message != nil {
					msg = *task.Data.Message
				}
				t.Fatalf("export task %s failed: %s", exportTaskID, msg)
			default:
				t.Logf("export task %s status=%s, waiting...", exportTaskID, task.Data.Status)
			}
		}
	}
}

func TestRecoverSourceFromURLImmediately(t *testing.T) {
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}

	sourceURL := os.Getenv("CLOUDCONVERT_RECOVER_SOURCE_URL")
	if sourceURL == "" {
		t.Skip("CLOUDCONVERT_RECOVER_SOURCE_URL not set, skipping test")
	}

	client := cloudconvert.NewClient(t.Context(), os.Getenv("CLOUDCONVERT_API_KEY"))

	job, err := client.NewJobBuilder().
		AddTask(cloudconvert.NewImportURLTask(importTaskName, &cloudconvert.ImportURLRequest{
			URL: sourceURL,
		})).
		AddTask(cloudconvert.NewExportURLTask(exportTaskName, &cloudconvert.ExportURLRequest{
			Input: importTaskName,
		})).
		Submit()
	if err != nil {
		t.Fatalf("submit import/url -> export/url recovery job failed: %v", err)
	}

	exportTaskID := job.TaskID(exportTaskName)
	if exportTaskID == "" {
		t.Fatalf("export task id not found for task name %s", exportTaskName)
	}

	t.Logf("created recovery export task id=%s", exportTaskID)
	t.Logf("check this task in CloudConvert dashboard and use TestGetTaskDetails/TestDownloadExportTask after it finishes")
}

func TestUploadCommandFaststartDownload(t *testing.T) {
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}

	const sourcePath = "test.flv"

	client := cloudconvert.NewClient(t.Context(), os.Getenv("CLOUDCONVERT_API_KEY"))
	f, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	job, err := client.NewJobBuilder().
		AddTask(cloudconvert.NewImportUploadTask(importTaskName, &cloudconvert.ImportUploadRequest{})).
		AddTask(cloudconvert.NewCommandTask(commandTaskName, &cloudconvert.CommandPayload{
			Input:     importTaskName,
			Engine:    "ffmpeg",
			Command:   "ffmpeg",
			Arguments: fmt.Sprintf("-i /input/%s/test.flv -map 0 -map_metadata 0 -c copy -movflags +faststart /output/output.mp4", importTaskName),
		})).
		AddTask(cloudconvert.NewExportURLTask(exportTaskName, &cloudconvert.ExportURLRequest{
			Input: commandTaskName,
		})).
		Submit()
	if err != nil {
		t.Fatal(err)
	}

	commandTaskID := job.TaskID(commandTaskName)
	exportTaskID := job.TaskID(exportTaskName)

	if commandTaskID == "" {
		t.Fatalf("command task id not found for task name %s", commandTaskName)
	}
	if exportTaskID == "" {
		t.Fatalf("export task id not found for task name %s", exportTaskName)
	}

	importTask := job.TaskData(importTaskName)

	if err := client.UploadFileToTask(f, importTask.Result.Form); err != nil {
		t.Fatal(err)
	}

	t.Logf("Upload successful, command task id=%s", commandTaskID)
	t.Logf("Export task id=%s (use CLOUDCONVERT_TASK_ID to download)", exportTaskID)
}
