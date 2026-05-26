package convert

import (
	"context"

	"github.com/eric2788/bilirec/pkg/db"
)

type Provider string

type GetActiveRecordings func() int

type ConvertManager interface {
	StartWorker(ctx context.Context, db *db.Client) error
	Enqueue(inputPath, outputPath, format string, deleteSource bool) (*TaskQueue, error)
	Cancel(taskID string) error
	ListInProgress() ([]*TaskQueue, error)
	InProgressSize() int // InProgressSize returns the number of tasks currently in progress, mainly for janitor service to check if the manager is idle.
}

type TaskQueue struct {
	Provider      Provider `json:"provider"` // "ffmpeg" or "cloudconvert"
	TaskID        string   `json:"task_id"`
	ConvertTaskID string   `json:"convert_task_id,omitempty"` // the real convert task id used by the cloudconvert only
	InputPath     string   `json:"input_path"`
	InputFileSize *int64   `json:"input_file_size,omitempty"`
	OutputPath    string   `json:"output_path"`
	InputFormat   string   `json:"input_format"`
	OutputFormat  string   `json:"output_format"`
	DeleteSource  bool     `json:"delete_source"`
}
