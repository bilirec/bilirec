package swr

import (
	"fmt"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"golang.org/x/sync/singleflight"
)

type entry[V any] struct {
	Value     V
	FetchedAt time.Time
}

type Cache[K comparable, V any] struct {
	cache   *ttlcache.Cache[K, *entry[V]]
	softTTL time.Duration
	group   singleflight.Group
}

func NewCache[K comparable, V any](softTTL, hardTTL time.Duration, capacity uint64) *Cache[K, V] {
	return &Cache[K, V]{
		cache: ttlcache.New(
			ttlcache.WithTTL[K, *entry[V]](hardTTL),
			ttlcache.WithCapacity[K, *entry[V]](capacity),
			ttlcache.WithDisableTouchOnHit[K, *entry[V]](),
		),
		softTTL: softTTL,
	}
}

func (c *Cache[K, V]) Start() {
	go c.cache.Start()
}

func (c *Cache[K, V]) Stop() {
	c.cache.Stop()
}

func (c *Cache[K, V]) DeleteAll() {
	c.cache.DeleteAll()
}

func (c *Cache[K, V]) Set(key K, value V) {
	c.cache.Set(key, &entry[V]{
		Value:     value,
		FetchedAt: time.Now(),
	}, ttlcache.DefaultTTL)
}

func (c *Cache[K, V]) Get(key K) (value V, ok bool, stale bool) {
	item := c.cache.Get(key)
	if item == nil || item.Value() == nil {
		return value, false, false
	}
	e := item.Value()
	return e.Value, true, time.Since(e.FetchedAt) > c.softTTL
}

func (c *Cache[K, V]) Load(key K, loader func() (V, error)) (V, error) {
	result, err, _ := c.group.Do(fmt.Sprint(key), func() (any, error) {
		v, loadErr := loader()
		if loadErr != nil {
			var zero V
			return zero, loadErr
		}
		c.Set(key, v)
		return v, nil
	})
	if err != nil {
		var zero V
		return zero, err
	}
	v, _ := result.(V)
	return v, nil
}

func (c *Cache[K, V]) RevalidateAsync(key K, loader func() (V, error)) {
	go func() {
		_, _, _ = c.group.Do(fmt.Sprint(key), func() (any, error) {
			v, err := loader()
			if err != nil {
				return nil, err
			}
			c.Set(key, v)
			return v, nil
		})
	}()
}
