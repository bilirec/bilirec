package ds

type mapSet[T comparable] struct {
	data map[T]void
}

func (s *mapSet[T]) Add(item T) bool {
	if s.Contains(item) {
		return true
	}
	s.data[item] = empty
	return false
}

func (s *mapSet[T]) Remove(item T) bool {
	if !s.Contains(item) {
		return false
	}
	delete(s.data, item)
	return true
}

func (s *mapSet[T]) Contains(item T) bool {
	_, exists := s.data[item]
	return exists
}

func (s *mapSet[T]) Size() int {
	return len(s.data)
}

func (s *mapSet[T]) ToSlice() []T {
	slice := make([]T, 0, len(s.data))
	for item := range s.data {
		slice = append(slice, item)
	}
	return slice
}

func (s *mapSet[T]) Clear() {
	s.data = make(map[T]void)
}
