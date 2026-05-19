package ds

import "sync"

type syncedSet[T comparable] struct {
	set  Set[T]
	lock sync.RWMutex
}

func (s *syncedSet[T]) Add(item T) bool {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.set.Add(item)
}

func (s *syncedSet[T]) Remove(item T) bool {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.set.Remove(item)
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
