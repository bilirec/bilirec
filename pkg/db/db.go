package db

import (
	"fmt"
	"os"
	"path/filepath"

	"go.etcd.io/bbolt"
)

type Client struct {
	BoltDB *bbolt.DB
}

// DefaultOptions returns the standard bbolt options used across the application
func DefaultOptions() *bbolt.Options {
	return &bbolt.Options{
		PageSize:     16 * 1024,
		NoGrowSync:   true,
		FreelistType: bbolt.FreelistArrayType,
	}
}

// Open opens a bbolt database with default options
func Open(dbPath string) (*Client, error) {
	return OpenWithOptions(dbPath, DefaultOptions())
}

// OpenWithOptions opens a bbolt database with custom options
func OpenWithOptions(dbPath string, opts *bbolt.Options) (*Client, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建数据库目录失败：%w", err)
	}

	db, err := bbolt.Open(dbPath, 0600, opts)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败：%w", err)
	}

	return &Client{BoltDB: db}, nil
}

func (c *Client) Close() error {
	return c.BoltDB.Close()
}