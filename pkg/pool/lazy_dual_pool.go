package pool

import (
	"sync"
	"time"
)

// LazyDualPool manages a default pool and an optional high-tier pool.
// The high-tier pool is lazily initialized and released when idle.
type LazyDualPool[T comparable] struct {
	mu sync.Mutex

	defaultPool T
	highPool    T

	highRef      int
	highLastUsed time.Time
	idleTTL      time.Duration
	cleanupTimer *time.Timer

	newDefault func() T
	newHigh    func() T
}

func NewLazyDualPool[T comparable](idleTTL time.Duration, newDefault func() T, newHigh func() T) *LazyDualPool[T] {
	return &LazyDualPool[T]{
		idleTTL:    idleTTL,
		newDefault: newDefault,
		newHigh:    newHigh,
	}
}

func (m *LazyDualPool[T]) Default() T {
	m.mu.Lock()
	defer m.mu.Unlock()
	var zero T
	if m.defaultPool == zero {
		m.defaultPool = m.newDefault()
	}
	return m.defaultPool
}

func (m *LazyDualPool[T]) MaybeHigh() T {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.highPool
}

func (m *LazyDualPool[T]) Acquire(useHigh bool) (T, func()) {
	if !useHigh {
		return m.Default(), func() {}
	}

	m.mu.Lock()
	m.stopCleanupTimerLocked()
	var zero T
	if m.highPool == zero {
		m.highPool = m.newHigh()
	}
	m.highRef++
	m.highLastUsed = time.Now()
	hp := m.highPool
	m.mu.Unlock()

	return hp, func() {
		m.mu.Lock()
		if m.highRef > 0 {
			m.highRef--
		}
		m.highLastUsed = time.Now()
		if m.highRef == 0 {
			m.scheduleCleanupLocked(m.idleTTL)
		}
		m.mu.Unlock()
	}
}

func (m *LazyDualPool[T]) TryCleanup(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var zero T
	if m.highPool == zero || m.highRef > 0 || m.highLastUsed.IsZero() {
		return
	}
	elapsed := now.Sub(m.highLastUsed)
	if elapsed < m.idleTTL {
		m.scheduleCleanupLocked(m.idleTTL - elapsed)
		return
	}
	m.stopCleanupTimerLocked()
	m.highPool = zero
}

func (m *LazyDualPool[T]) stopCleanupTimerLocked() {
	if m.cleanupTimer != nil {
		m.cleanupTimer.Stop()
		m.cleanupTimer = nil
	}
}

func (m *LazyDualPool[T]) scheduleCleanupLocked(delay time.Duration) {
	if delay < 0 {
		delay = 0
	}
	m.stopCleanupTimerLocked()
	m.cleanupTimer = time.AfterFunc(delay, func() {
		m.TryCleanup(time.Now())
	})
}
