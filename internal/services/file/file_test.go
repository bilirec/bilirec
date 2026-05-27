package file_test

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/file"
	"github.com/bilirec/bilirec/internal/services/path"
	"github.com/bilirec/bilirec/utils"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestListTree(t *testing.T) {

	os.Setenv("OUTPUT_DIR", t.TempDir())

	var fileService *file.Service
	var cfg *config.Config

	app := fxtest.New(t,
		config.Module,
		fx.Provide(path.NewService),
		fx.Provide(file.NewService),
		fx.Populate(&fileService),
		fx.Populate(&cfg),
	)

	app.RequireStart()
	defer app.RequireStop()

	t.Logf("using recording path: %s", cfg.OutputDir)

	for range 15 {
		dir := fmt.Sprintf("%s%c%s", cfg.OutputDir, os.PathSeparator, "logs")
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			t.Fatalf("failed to create logs directory: %v", err)
		}
		f1, err := os.CreateTemp(dir, "test-*.log")
		if err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}
		reader := file.NewTempReader(f1)
		defer reader.Close()
	}

	for range 15 {
		dir := fmt.Sprintf("%s%c%s", cfg.OutputDir, os.PathSeparator, "videos")
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			t.Fatalf("failed to create videos directory: %v", err)
		}
		f2, err := os.CreateTemp(dir, "test-*.mp4")
		if err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}
		reader := file.NewTempReader(f2)
		defer reader.Close()
	}

	for range 15 {
		dir := fmt.Sprintf("%s%c%s", cfg.OutputDir, os.PathSeparator, "texts")
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			t.Fatalf("failed to create texts directory: %v", err)
		}
		f3, err := os.CreateTemp(dir, "test-*.txt")
		if err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}
		reader := file.NewTempReader(f3)
		defer reader.Close()
	}

	trees, err := fileService.ListTree("/")
	if err != nil {
		t.Fatalf("failed to list tree: %v", err)
	}

	for _, tree := range trees {
		t.Logf("found directory: %v", tree.Path)
		if !tree.IsDir {
			t.Fatalf("expected directory but got file: %s", tree.Path)
		}
		subTrees, err := fileService.ListTree(tree.Path)
		if err != nil {
			t.Fatalf("failed to list sub tree: %v", err)
		}
		for _, subTree := range subTrees {
			if subTree.IsDir {
				t.Fatalf("expected file but got directory: %s", subTree.Path)
			} else if !strings.HasPrefix(subTree.Path, tree.Path) {
				t.Fatalf("expected file path to start with %s but got %s", tree.Path, subTree.Path)
			}
			t.Logf("  found file: %v (size: %d bytes)", subTree.Path, subTree.Size)
		}
		t.Logf("total files found in directory %s: %d", tree.Path, len(subTrees))
	}

	t.Logf("total directories found: %d", len(trees))
}

func TestValidatePath(t *testing.T) {
	os.Setenv("OUTPUT_DIR", t.TempDir())

	var fileService *file.Service

	app := fxtest.New(t,
		config.Module,
		fx.Provide(path.NewService),
		fx.Provide(file.NewService),
		fx.Populate(&fileService),
	)

	app.RequireStart()
	defer app.RequireStop()

	invalidPaths := []string{
		"../",
		"//absolute/",
		"subdir/../../",
		"/etc/",
	}

	for _, path := range invalidPaths {
		_, err := fileService.ListTree(path)
		if err == nil {
			t.Errorf("expected error for invalid path %s, but got none", path)
		} else {
			t.Logf("correctly got error for invalid path %s: %v", path, err)
		}
	}
}

func TestDownloadStream(t *testing.T) {
	tempDir := t.TempDir()

	os.Setenv("OUTPUT_DIR", tempDir)
	var fileService *file.Service

	app := fxtest.New(t,
		config.Module,
		fx.Provide(path.NewService),
		fx.Provide(file.NewService),
		fx.Populate(&fileService),
	)

	app.RequireStart()
	defer app.RequireStop()

	// create a temp flv file
	f, err := os.Create(tempDir + string(os.PathSeparator) + "test.flv")
	if err != nil {
		t.Fatalf("failed to create temp flv file: %v", err)
	}
	f.WriteString(utils.RandomHexStringMust(512))
	f.Close() // must close so that file service can access it

	reader, _, err := fileService.GetFileStream("test.flv")
	if err != nil {
		t.Fatalf("failed to get file stream: %v", err)
	}
	defer reader.Close()
	// simulate bytes stream read

	buf := make([]byte, 64)
	for {
		data, err := reader.Read(buf)
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("failed to read from stream: %v", err)
		}
		t.Logf("read %d bytes from stream", data)
	}
}

func TestDeleteFiles(t *testing.T) {
	tempDir := t.TempDir()

	os.Setenv("OUTPUT_DIR", tempDir)
	var fileService *file.Service

	app := fxtest.New(t,
		config.Module,
		fx.Provide(path.NewService),
		fx.Provide(file.NewService),
		fx.Populate(&fileService),
	)

	app.RequireStart()
	defer app.RequireStop()

	// Create nested directory structure with files
	testFiles := []string{}

	// Create files in root
	for i := range 3 {
		filename := fmt.Sprintf("root-file-%d.txt", i)
		filepath := tempDir + string(os.PathSeparator) + filename
		f, err := os.Create(filepath)
		if err != nil {
			t.Fatalf("failed to create root file: %v", err)
		}
		f.WriteString(utils.RandomHexStringMust(64))
		f.Close()
		testFiles = append(testFiles, filename)
	}

	// Create nested directory with files
	nestedDir := "recordings"
	nestedPath := fmt.Sprintf("%s%c%s", tempDir, os.PathSeparator, nestedDir)
	if err := os.MkdirAll(nestedPath, os.ModePerm); err != nil {
		t.Fatalf("failed to create nested directory: %v", err)
	}

	for i := range 4 {
		filename := fmt.Sprintf("%s%crecord-%d.flv", nestedDir, os.PathSeparator, i)
		filepath := tempDir + string(os.PathSeparator) + filename
		f, err := os.Create(filepath)
		if err != nil {
			t.Fatalf("failed to create nested file: %v", err)
		}
		f.WriteString(utils.RandomHexStringMust(128))
		f.Close()
		testFiles = append(testFiles, filename)
	}

	// Create deeper nested directory with files
	deeperDir := fmt.Sprintf("%s%clive-streams", nestedDir, os.PathSeparator)
	deeperPath := fmt.Sprintf("%s%c%s", tempDir, os.PathSeparator, deeperDir)
	if err := os.MkdirAll(deeperPath, os.ModePerm); err != nil {
		t.Fatalf("failed to create deeper directory: %v", err)
	}

	for i := range 3 {
		filename := fmt.Sprintf("%s%cstream-%d.mp4", deeperDir, os.PathSeparator, i)
		filepath := tempDir + string(os.PathSeparator) + filename
		f, err := os.Create(filepath)
		if err != nil {
			t.Fatalf("failed to create deeper nested file: %v", err)
		}
		f.WriteString(utils.RandomHexStringMust(256))
		f.Close()
		testFiles = append(testFiles, filename)
	}

	t.Logf("created %d test files across multiple directory levels", len(testFiles))

	// Verify all files exist
	for _, filename := range testFiles {
		fullPath := tempDir + string(os.PathSeparator) + filename
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			t.Fatalf("test file %s should exist but doesn't", filename)
		}
	}

	// Delete all files
	err := fileService.DeleteFiles(testFiles...)
	if err != nil {
		t.Fatalf("failed to delete files: %v", err)
	}

	// Verify all files are deleted
	for _, filename := range testFiles {
		fullPath := tempDir + string(os.PathSeparator) + filename
		if _, err := os.Stat(fullPath); !os.IsNotExist(err) {
			t.Errorf("file %s should be deleted but still exists", filename)
		}
	}

	// Verify directories still exist (only files should be deleted)
	if _, err := os.Stat(nestedPath); os.IsNotExist(err) {
		t.Errorf("directory %s should still exist after deleting files", nestedDir)
	}
	if _, err := os.Stat(deeperPath); os.IsNotExist(err) {
		t.Errorf("directory %s should still exist after deleting files", deeperDir)
	}

	t.Logf("successfully deleted %d files across multiple directory levels", len(testFiles))
}

func TestDeleteDirectory(t *testing.T) {
	tempDir := t.TempDir()

	os.Setenv("OUTPUT_DIR", tempDir)
	var fileService *file.Service

	app := fxtest.New(t,
		config.Module,
		fx.Provide(path.NewService),
		fx.Provide(file.NewService),
		fx.Populate(&fileService),
	)

	app.RequireStart()
	defer app.RequireStop()

	// Create complex nested directory structure
	testDir := "archive"
	dirPath := fmt.Sprintf("%s%c%s", tempDir, os.PathSeparator, testDir)
	if err := os.MkdirAll(dirPath, os.ModePerm); err != nil {
		t.Fatalf("failed to create test directory: %v", err)
	}

	// Add files to root directory
	for i := range 3 {
		f, err := os.CreateTemp(dirPath, fmt.Sprintf("root-file-%d-*.txt", i))
		if err != nil {
			t.Fatalf("failed to create temp file in root directory: %v", err)
		}
		f.WriteString(utils.RandomHexStringMust(32))
		f.Close()
	}

	// Create first level subdirectories with files
	for roomId := 1; roomId <= 3; roomId++ {
		roomDir := fmt.Sprintf("%s%croom-%d", dirPath, os.PathSeparator, roomId)
		if err := os.MkdirAll(roomDir, os.ModePerm); err != nil {
			t.Fatalf("failed to create room directory: %v", err)
		}

		// Add recording files to each room directory
		for i := range 2 {
			f, err := os.CreateTemp(roomDir, fmt.Sprintf("recording-%d-*.flv", i))
			if err != nil {
				t.Fatalf("failed to create recording file: %v", err)
			}
			f.WriteString(utils.RandomHexStringMust(64))
			f.Close()
		}

		// Create deeper nested logs directory inside each room
		logsDir := fmt.Sprintf("%s%clogs", roomDir, os.PathSeparator)
		if err := os.MkdirAll(logsDir, os.ModePerm); err != nil {
			t.Fatalf("failed to create logs directory: %v", err)
		}

		// Add log files
		for i := range 2 {
			f, err := os.CreateTemp(logsDir, fmt.Sprintf("log-%d-*.txt", i))
			if err != nil {
				t.Fatalf("failed to create log file: %v", err)
			}
			f.WriteString(utils.RandomHexStringMust(16))
			f.Close()
		}
	}

	// Create an empty subdirectory
	emptyDir := fmt.Sprintf("%s%cempty-folder", dirPath, os.PathSeparator)
	if err := os.MkdirAll(emptyDir, os.ModePerm); err != nil {
		t.Fatalf("failed to create empty directory: %v", err)
	}

	// Verify directory structure exists
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		t.Fatalf("test directory should exist but doesn't")
	}

	// Count total files and directories before deletion
	var fileCount, dirCount int
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			dirCount++
		} else {
			fileCount++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk directory: %v", err)
	}
	t.Logf("directory structure: %d directories, %d files", dirCount, fileCount)

	// Delete entire directory tree
	err = fileService.DeleteDirectory(testDir)
	if err != nil {
		t.Fatalf("failed to delete directory: %v", err)
	}

	// Verify directory is completely deleted
	if _, err := os.Stat(dirPath); !os.IsNotExist(err) {
		t.Errorf("directory %s should be deleted but still exists", testDir)
	}

	t.Logf("successfully deleted directory %s with all %d subdirectories and %d files", testDir, dirCount-1, fileCount)

	// os.RemoveAll return nil anyways, no need to test non-existing directory deletion
}
