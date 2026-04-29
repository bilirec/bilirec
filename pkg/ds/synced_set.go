package ds

import "sync"

type syncedSet[T comparable] struct {
	set  Set[T]
	lock sync.RWMutex
}

func (s *syncedSet[T]) Add(item T) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.set.Add(item)
}

func (s *syncedSet[T]) Remove(item T) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.set.Remove(item)
}

func (s *syncedSet[T]) Contains(item T) bool {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.set.Contains(item)
}

func (s *syncedSet[T]) Size() int {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.set.Size()
}

func (s *syncedSet[T]) ToSlice() []T {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.set.ToSlice()
}

func (s *syncedSet[T]) Clear() {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.set.Clear()
}

func (s *syncedSet[T]) LoadAndStore(item T) bool {
	s.lock.Lock()
	defer s.lock.Unlock()
	if s.set.Contains(item) {
		return true
	}
	s.set.Add(item)
	return false
}

func (s *syncedSet[T]) LoadAndDelete(item T) bool {
	s.lock.Lock()
	defer s.lock.Unlock()
	if !s.set.Contains(item) {
		return false
	}
	s.set.Remove(item)
	return true
}
