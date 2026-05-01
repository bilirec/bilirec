package ds

// Stack is a generic LIFO data structure. Not safe for concurrent use.
type Stack[T any] struct {
	data []T
}

// Push adds an element to the top of the stack.
func (s *Stack[T]) Push(v T) {
	s.data = append(s.data, v)
}

// Transition pops the previous top, compares it with next, then pushes next.
// Returns true if next differs from the previous top (a real transition occurred).
// If the stack was empty, next is pushed and true is returned.
func (s *Stack[T]) Transition(next T, eq func(a, b T) bool) bool {
	prev, hasPrev := s.Pop()
	s.Push(next)
	if !hasPrev {
		return true
	}
	return !eq(prev, next)
}

// Pop removes and returns the top element.
// Returns the zero value and false if the stack is empty.
func (s *Stack[T]) Pop() (T, bool) {
	if len(s.data) == 0 {
		var zero T
		return zero, false
	}
	top := s.data[len(s.data)-1]
	s.data = s.data[:len(s.data)-1]
	return top, true
}

// Top returns the top element without removing it.
// Returns the zero value and false if the stack is empty.
func (s *Stack[T]) Top() (T, bool) {
	if len(s.data) == 0 {
		var zero T
		return zero, false
	}
	return s.data[len(s.data)-1], true
}

// Len returns the number of elements in the stack.
func (s *Stack[T]) Len() int {
	return len(s.data)
}

// Clear removes all elements from the stack.
func (s *Stack[T]) Clear() {
	s.data = s.data[:0]
}
