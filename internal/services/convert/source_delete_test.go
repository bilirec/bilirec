package convert

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/backoff"
	"github.com/sirupsen/logrus"
)

func TestMarkSourceForManualDelete(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "c.flv")
	if err := os.WriteFile(src, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	renamed, err := markSourceForManualDelete(src)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "c_轉換完成可刪除.flv")
	if renamed != want {
		t.Fatalf("renamed path = %q, want %q", renamed, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected renamed file: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("expected source removed, stat err = %v", err)
	}
}

func TestMarkSourceForManualDelete_CollisionSuffix(t *testing.T) {
	dir := t.TempDir()
	occupied := filepath.Join(dir, "c_轉換完成可刪除.flv")
	if err := os.WriteFile(occupied, []byte("occupied"), 0644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "c.flv")
	if err := os.WriteFile(src, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	renamed, err := markSourceForManualDelete(src)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "c_轉換完成可刪除-2.flv")
	if renamed != want {
		t.Fatalf("renamed path = %q, want %q", renamed, want)
	}
}

func TestSchedule_DedupesSamePath(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "dup.flv")
	if err := os.WriteFile(src, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	d := newSourceDeleter(ctx)
	log := logrus.NewEntry(logrus.New())

	block := make(chan struct{})
	var calls atomic.Int32
	origRemove := removeSourceFile
	removeSourceFile = func(path string) error {
		calls.Add(1)
		<-block
		return origRemove(path)
	}
	t.Cleanup(func() { removeSourceFile = origRemove })

	queue := &TaskQueue{
		TaskID:       "task-1",
		InputPath:    src,
		OutputPath:   filepath.Join(dir, "dup.mp4"),
		DeleteSource: true,
	}

	d.Schedule(queue, log)
	time.Sleep(50 * time.Millisecond)
	d.Schedule(queue, log)

	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 delete attempt, got %d", got)
	}

	close(block)
	d.Wait()
}

func TestDelete_SucceedsWhenFileGone(t *testing.T) {
	ctx := context.Background()
	d := newSourceDeleter(ctx)
	log := logrus.NewEntry(logrus.New())

	missing := filepath.Join(t.TempDir(), "missing.flv")
	queue := &TaskQueue{
		TaskID:       "task-missing",
		InputPath:    missing,
		OutputPath:   missing + ".mp4",
		DeleteSource: true,
	}

	d.Schedule(queue, log)
	d.Wait()

	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("expected missing file to stay absent, err=%v", err)
	}
}

func TestDelete_RenamesAfterMaxAttempts(t *testing.T) {
	origBackoff := newSourceDeleteBackoff
	newSourceDeleteBackoff = func() *backoff.Expotential {
		return backoff.NewExpotential(1*time.Millisecond, 2, 5*time.Millisecond)
	}
	t.Cleanup(func() { newSourceDeleteBackoff = origBackoff })

	dir := t.TempDir()
	src := filepath.Join(dir, "locked.flv")
	if err := os.WriteFile(src, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	d := newSourceDeleter(ctx)
	log := logrus.NewEntry(logrus.New())

	origRemove := removeSourceFile
	removeSourceFile = func(path string) error {
		return errors.New("permission denied")
	}
	t.Cleanup(func() { removeSourceFile = origRemove })

	queue := &TaskQueue{
		TaskID:       "task-fail",
		InputPath:    src,
		OutputPath:   filepath.Join(dir, "locked.mp4"),
		DeleteSource: true,
	}

	d.Schedule(queue, log)
	d.Wait()

	want := filepath.Join(dir, "locked_轉換完成可刪除.flv")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected renamed file %s: %v", want, err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("expected source renamed away, err=%v", err)
	}
}
