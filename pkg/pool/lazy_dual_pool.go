package pool

import (
	"sync"
	"time"
)

// DefaultLazyDualPoolIdleTTL is how long a tier with no active leases may stay cached before release.
const DefaultLazyDualPoolIdleTTL = 5 * time.Minute

// LazyDualPool manages a default pool and an optional high-tier pool.
// Each tier is lazily initialized and released when idle with no active leases.
type LazyDualPool[T comparable] struct {
	mu sync.Mutex

	idleTTL time.Duration

	defaultTier poolTier[T]
	highTier    poolTier[T]

	newDefault func() T
	newHigh    func() T
}

type poolTier[T comparable] struct {
	pool         T
	ref          int
	lastUsed     time.Time
	cleanupTimer *time.Timer
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
	return m.ensureTierLocked(&m.defaultTier, m.newDefault)
}

func (m *LazyDualPool[T]) MaybeDefault() T {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.defaultTier.pool
}

func (m *LazyDualPool[T]) MaybeHigh() T {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.highTier.pool
}

func (m *LazyDualPool[T]) Acquire(useHigh bool) (T, func()) {
	if !useHigh {
		return m.acquireTier(&m.defaultTier, m.newDefault)
	}
	return m.acquireTier(&m.highTier, m.newHigh)
}

func (m *LazyDualPool[T]) TryCleanup(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tryCleanupTierLocked(&m.defaultTier, now)
	m.tryCleanupTierLocked(&m.highTier, now)
}

func (m *LazyDualPool[T]) acquireTier(tier *poolTier[T], newPool func() T) (T, func()) {
	m.mu.Lock()
	pool := m.ensureTierLocked(tier, newPool)
	tier.ref++
	tier.lastUsed = time.Now()
	m.stopCleanupTimerLocked(tier)
	m.mu.Unlock()

	return pool, func() {
		m.mu.Lock()
		if tier.ref > 0 {
			tier.ref--
		}
		tier.lastUsed = time.Now()
		if tier.ref == 0 {
			m.scheduleCleanupLocked(tier, m.idleTTL)
		}
		m.mu.Unlock()
	}
}

func (m *LazyDualPool[T]) ensureTierLocked(tier *poolTier[T], newPool func() T) T {
	var zero T
	if tier.pool == zero {
		tier.pool = newPool()
	}
	return tier.pool
}

func (m *LazyDualPool[T]) tryCleanupTierLocked(tier *poolTier[T], now time.Time) {
	var zero T
	if tier.pool == zero || tier.ref > 0 || tier.lastUsed.IsZero() {
		return
	}
	elapsed := now.Sub(tier.lastUsed)
	if elapsed < m.idleTTL {
		m.scheduleCleanupLocked(tier, m.idleTTL-elapsed)
		return
	}
	m.stopCleanupTimerLocked(tier)
	tier.pool = zero
}

func (m *LazyDualPool[T]) stopCleanupTimerLocked(tier *poolTier[T]) {
	if tier.cleanupTimer != nil {
		tier.cleanupTimer.Stop()
		tier.cleanupTimer = nil
	}
}

func (m *LazyDualPool[T]) scheduleCleanupLocked(tier *poolTier[T], delay time.Duration) {
	if delay < 0 {
		delay = 0
	}
	m.stopCleanupTimerLocked(tier)
	tier.cleanupTimer = time.AfterFunc(delay, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.tryCleanupTierLocked(tier, time.Now())
	})
}
