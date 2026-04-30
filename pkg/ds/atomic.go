package ds

import "sync/atomic"

// Atomic is a type-safe wrapper around atomic.Value.
// The zero value is ready to use.
type Atomic[T any] struct {
	v atomic.Value
}

// Store atomically replaces the current value.
func (a *Atomic[T]) Store(value T) {
	a.v.Store(value)
}

// Load returns the current value and whether it has been initialized.
func (a *Atomic[T]) Load() (T, bool) {
	raw := a.v.Load()
	if raw == nil {
		var zero T
		return zero, false
	}
	return raw.(T), true
}

// MustLoad returns the current value and panics if it has not been initialized.
func (a *Atomic[T]) MustLoad() T {
	v, ok := a.Load()
	if !ok {
		panic("atomic value is not initialized")
	}
	return v
}

// LoadOr returns the current value or fallback when uninitialized.
func (a *Atomic[T]) LoadOr(fallback T) T {
	v, ok := a.Load()
	if ok {
		return v
	}
	return fallback
}

// Swap atomically stores value and returns the previous value.
// If the atomic was uninitialized, loaded is false and old is zero value.
func (a *Atomic[T]) Swap(value T) (old T, loaded bool) {
	raw := a.v.Swap(value)
	if raw == nil {
		var zero T
		return zero, false
	}
	return raw.(T), true
}

// CompareAndSwap executes CAS on the stored value.
// It returns false if the atomic was uninitialized.
// Note: T must be comparable at runtime when using this method.
func (a *Atomic[T]) CompareAndSwap(old, new T) bool {
	if raw := a.v.Load(); raw == nil {
		return false
	}
	return a.v.CompareAndSwap(old, new)
}
