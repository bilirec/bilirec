package db

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"go.etcd.io/bbolt"
)

func TestBBoltPanic(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tmp_meta.db")
	db, err := bbolt.Open(dbPath, 0600, &bbolt.Options{PageSize: 4096})
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte("bucket"))
		if err != nil {
			return err
		}
		value := make([]byte, 2048)
		for i := 0; i < 100; i++ {
			if err := bucket.Put([]byte(fmt.Sprintf("key-%d", i)), value); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db failed: %v", err)
	}
	f, err := os.Open(dbPath)
	if err != nil {
		t.Fatalf("open db file failed: %v", err)
	}
	defer f.Close()
	buf := make([]byte, 4096)
	if _, err := f.ReadAt(buf, 0); err != nil {
		t.Fatalf("read db file failed: %v", err)
	}
	t.Logf("offset 0 magic=%x pageSize=%d freelist=%d checksum=%x\n", binary.LittleEndian.Uint32(buf[16:20]), binary.LittleEndian.Uint32(buf[24:28]), binary.LittleEndian.Uint64(buf[48:56]), binary.LittleEndian.Uint64(buf[56:64]))
	if _, err := f.ReadAt(buf, 4096); err != nil {
		t.Fatalf("read db file failed: %v", err)
	}
	t.Logf("offset 1 magic=%x pageSize=%d freelist=%d checksum=%x\n", binary.LittleEndian.Uint32(buf[16:20]), binary.LittleEndian.Uint32(buf[24:28]), binary.LittleEndian.Uint64(buf[48:56]), binary.LittleEndian.Uint64(buf[56:64]))
}
