package db

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	goetcdio_bbolt "go.etcd.io/bbolt"
)

func TestDBOpenCloseStress(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "stress.db")
	const iterations = 20
	workers := runtime.NumCPU() * 2

	for i := 0; i < iterations; i++ {
		client, err := Open(dbPath)
		if err != nil {
			t.Fatalf("iteration %d: open db failed: %v", i, err)
		}

		bucket, err := client.Bucket("stress")
		if err != nil {
			_ = client.Close()
			t.Fatalf("iteration %d: create bucket failed: %v", i, err)
		}

		var wg sync.WaitGroup
		errCh := make(chan error, 1)
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				for j := 0; j < 50; j++ {
					key := []byte(fmt.Sprintf("k-%d-%d", workerID, j))
					if err := bucket.Update(func(b *goetcdio_bbolt.Bucket) error {
						return b.Put(key, []byte("value"))
					}); err != nil {
						select {
						case errCh <- err:
						default:
						}
						return
					}

					exists, err := bucket.Exists(key)
					if err != nil {
						select {
						case errCh <- err:
						default:
						}
						return
					}
					if !exists {
						select {
						case errCh <- fmt.Errorf("missing key %s", key):
						default:
						}
						return
					}
				}
			}(w)
		}

		wg.Wait()
		select {
		case err := <-errCh:
			_ = client.Close()
			t.Fatalf("iteration %d: concurrent bucket operation failed: %v", i, err)
		default:
		}

		if err := client.Close(); err != nil {
			t.Fatalf("iteration %d: close db failed: %v", i, err)
		}
	}
}

func TestDBConcurrentWriterAndShutdown(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "shutdown.db")
	client, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			t.Fatalf("close db failed: %v", cerr)
		}
	}()

	bucket, err := client.Bucket("shutdown")
	if err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	errCh := make(chan error, 1)
	workers := runtime.NumCPU() * 2

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for count := 0; count < 200; count++ {
				select {
				case <-ctx.Done():
					return
				default:
				}

				key := []byte(fmt.Sprintf("shutdown-%d-%d", workerID, count))
				if err := bucket.Update(func(b *goetcdio_bbolt.Bucket) error {
					return b.Put(key, []byte("ok"))
				}); err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
			}
		}(w)
	}

	time.Sleep(100 * time.Millisecond)
	cancel()
	wg.Wait()

	select {
	case err := <-errCh:
		t.Fatalf("concurrent writer failed: %v", err)
	default:
	}
}
func TestBboltCorruptedFreelistCausesPageAlreadyFreed(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "corrupt.db")

	db, err := goetcdio_bbolt.Open(dbPath, 0600, &goetcdio_bbolt.Options{PageSize: 4096})
	if err != nil {
		t.Fatalf("open bbolt db failed: %v", err)
	}

	err = db.Update(func(tx *goetcdio_bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte("bucket"))
		if err != nil {
			return err
		}
		value := make([]byte, 2048)
		for i := 0; i < 300; i++ {
			if err := bucket.Put([]byte(fmt.Sprintf("key-%d", i)), value); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		t.Fatalf("prepare db failed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db failed: %v", err)
	}

	if err := corruptFreelistToSelfReference(dbPath, 4096, 5); err != nil {
		t.Fatalf("corrupt freelist failed: %v", err)
	}

	db, err = goetcdio_bbolt.Open(dbPath, 0600, &goetcdio_bbolt.Options{PageSize: 4096})
	if err != nil {
		t.Fatalf("open corrupted db failed: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()

	didPanic := false
	var panicValue any
	func() {
		defer func() {
			if r := recover(); r != nil {
				didPanic = true
				panicValue = r
			}
		}()
		_ = db.Update(func(tx *goetcdio_bbolt.Tx) error {
			b := tx.Bucket([]byte("bucket"))
			if b == nil {
				return fmt.Errorf("bucket missing")
			}
			return b.Put([]byte("key-0"), make([]byte, 2048))
		})
	}()

	if !didPanic {
		t.Fatal("expected panic due to corrupted freelist, but did not panic")
	}

	if msg, ok := panicValue.(string); !ok || !containsPageAlreadyFreed(msg) {
		t.Fatalf("unexpected panic value: %#v", panicValue)
	}
}

func corruptFreelistToSelfReference(path string, pageSize int, freelistPageID uint64) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	// Ensure the file is large enough to contain the target freelist page.
	minSize := int64((freelistPageID + 1) * uint64(pageSize))
	if info, err := f.Stat(); err != nil {
		return err
	} else if info.Size() < minSize {
		if _, err := f.Seek(minSize-1, io.SeekStart); err != nil {
			return err
		}
		if _, err := f.Write([]byte{0}); err != nil {
			return err
		}
	}

	if err := patchMetaFreelist(f, pageSize, freelistPageID); err != nil {
		return err
	}
	if err := writeSelfReferencingFreelistPage(f, pageSize, freelistPageID); err != nil {
		return err
	}
	return nil
}

func patchMetaFreelist(f *os.File, pageSize int, freelistPageID uint64) error {
	buf := make([]byte, pageSize)
	patched := false

	for _, offset := range []int64{0, int64(pageSize)} {
		if _, err := f.ReadAt(buf, offset); err != nil {
			return err
		}
		if binary.LittleEndian.Uint32(buf[16:20]) != 0xED0CDAED {
			continue
		}
		if !validateMetaChecksum(buf[16:]) {
			continue
		}
		binary.LittleEndian.PutUint64(buf[48:56], freelistPageID)
		binary.LittleEndian.PutUint64(buf[72:80], computeMetaChecksum(buf[16:72]))
		if _, err := f.WriteAt(buf, offset); err != nil {
			return err
		}
		patched = true
	}

	if !patched {
		return fmt.Errorf("no valid meta page found")
	}
	return nil
}

func writeSelfReferencingFreelistPage(f *os.File, pageSize int, pageID uint64) error {
	buf := make([]byte, pageSize)
	binary.LittleEndian.PutUint64(buf[0:8], pageID)
	binary.LittleEndian.PutUint16(buf[8:10], 0x10)
	binary.LittleEndian.PutUint16(buf[10:12], 1)
	binary.LittleEndian.PutUint32(buf[12:16], 0)
	binary.LittleEndian.PutUint64(buf[16:24], pageID)
	if _, err := f.WriteAt(buf, int64(pageID)*int64(pageSize)); err != nil {
		return err
	}
	return nil
}

func validateMetaChecksum(data []byte) bool {
	if len(data) < 64 {
		return false
	}
	expected := binary.LittleEndian.Uint64(data[56:64])
	return expected == computeMetaChecksum(data[:56])
}

func computeMetaChecksum(data []byte) uint64 {
	h := fnv.New64a()
	h.Write(data)
	return h.Sum64()
}

func containsPageAlreadyFreed(msg string) bool {
	return strings.Contains(msg, "page 5 already freed")
}
