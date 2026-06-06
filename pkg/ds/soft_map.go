package ds

import (
	"context"
	"time"

	"github.com/puzpuzpuz/xsync/v4"
)

type softValue[V any] struct {
	value     V
	deletedAt *time.Time
}

type SoftMap[K comparable, V any] struct {
	data *xsync.Map[K, softValue[V]]
}

func NewSoftMap[K comparable, V any]() *SoftMap[K, V] {
	return &SoftMap[K, V]{
		data: xsync.NewMap[K, softValue[V]](),
	}
}

func (m *SoftMap[K, V]) Store(key K, value V) {
	m.data.Store(key, softValue[V]{value: value})
}

func (m *SoftMap[K, V]) Load(key K) (V, bool) {
	if v, ok := m.data.Load(key); ok {
		if v.deletedAt == nil {
			return v.value, true
		}
	}
	var zero V
	return zero, false
}

func (m *SoftMap[K, V]) LoadStale(key K) (V, bool) {
	if v, ok := m.data.Load(key); ok {
		return v.value, true
	}
	var zero V
	return zero, false
}

func (m *SoftMap[K, V]) Delete(key K) {
	if v, ok := m.data.Load(key); ok {
		now := time.Now()
		m.data.Store(key, softValue[V]{value: v.value, deletedAt: &now})
	}
}

func (m *SoftMap[K, V]) HardDelete(key K) {
	m.data.Delete(key)
}

func (m *SoftMap[K, V]) Revive(key K) bool {
	if v, ok := m.data.Load(key); ok {
		if v.deletedAt != nil {
			v.deletedAt = nil
			m.data.Store(key, v)
			return true
		}
	}
	return false
}

func (m *SoftMap[K, V]) Cleanup(expiration time.Duration) {
	now := time.Now()
	m.data.Range(func(key K, value softValue[V]) bool {
		if value.deletedAt != nil && now.Sub(*value.deletedAt) > expiration {
			m.data.Delete(key)
		}
		return true
	})
}

func (m *SoftMap[K, V]) Range(f func(key K, value V) bool) {
	m.data.Range(func(key K, value softValue[V]) bool {
		if value.deletedAt == nil {
			return f(key, value.value)
		}
		return true
	})
}

func (m *SoftMap[K, V]) Len() int {
	count := 0
	m.data.Range(func(key K, value softValue[V]) bool {
		if value.deletedAt == nil {
			count++
		}
		return true
	})
	return count
}

func (m *SoftMap[K, V]) StartCleanupJob(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	for {
		select {
		case <-ticker.C:
			m.Cleanup(time.Minute)
		case <-ctx.Done():
			ticker.Stop()
			return
		}
	}
}
