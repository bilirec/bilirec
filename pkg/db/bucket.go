package db

import (
	"fmt"

	"go.etcd.io/bbolt"
)

type Bucket struct {
	*Client
	Name []byte
}

func (c *Client) Bucket(name string) (*Bucket, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed {
		return nil, fmt.Errorf("database is closed")
	}

	if err := c.db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(name))
		return err
	}); err != nil {
		return nil, err
	}
	return &Bucket{
		Client: c,
		Name:   []byte(name),
	}, nil
}

func (b *Bucket) Update(fn func(bucket *bbolt.Bucket) error) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return fmt.Errorf("database is closed")
	}

	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(b.Name)
		if bucket == nil {
			return fmt.Errorf("未找到 bucket %q", b.Name)
		}
		return fn(bucket)
	})
}

func (b *Bucket) View(fn func(bucket *bbolt.Bucket) error) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return fmt.Errorf("database is closed")
	}

	return b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(b.Name)
		if bucket == nil {
			return fmt.Errorf("未找到 bucket %q", b.Name)
		}
		return fn(bucket)
	})
}

func (b *Bucket) Put(key, value []byte) error {
	return b.Update(func(bucket *bbolt.Bucket) error {
		return bucket.Put(key, value)
	})
}

// Get retrieves the value for the given key.
// The returned byte slice is a copy of the data stored in the database.
// Recommended to use GetFunc to avoid extra allocations.
func (b *Bucket) Get(key []byte) ([]byte, error) {
	var value []byte
	err := b.GetFunc(key, func(v []byte) error {
		value = make([]byte, len(v))
		copy(value, v)
		return nil
	})
	return value, err
}

// GetFunc retrieves the value for the given key and passes it to the provided function.
// This avoids an extra allocation if the caller only needs to read the value
// which will boost performance.
func (b *Bucket) GetFunc(key []byte, fn func([]byte) error) error {
	return b.View(func(bucket *bbolt.Bucket) error {
		v := bucket.Get(key)
		if v != nil {
			return fn(v)
		}
		return nil
	})
}

func (b *Bucket) Delete(key []byte) error {
	return b.Update(func(bucket *bbolt.Bucket) error {
		return bucket.Delete(key)
	})
}

func (b *Bucket) ForEach(fn func(k, v []byte) error) error {
	return b.View(func(bucket *bbolt.Bucket) error {
		return bucket.ForEach(fn)
	})
}

func (b *Bucket) Exists(key []byte) (bool, error) {
	var exists bool
	err := b.View(func(bucket *bbolt.Bucket) error {
		v := bucket.Get(key)
		exists = v != nil
		return nil
	})
	return exists, err
}

func (b *Bucket) Count() (int, error) {
	var count int
	err := b.View(func(bucket *bbolt.Bucket) error {
		count = bucket.Stats().KeyN
		return nil
	})
	return count, err
}

func (b *Bucket) DeleteBucket() error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return fmt.Errorf("database is closed")
	}

	return b.db.Update(func(tx *bbolt.Tx) error {
		return tx.DeleteBucket(b.Name)
	})
}

func (b *Bucket) Close() error {
	return b.Client.Close()
}
