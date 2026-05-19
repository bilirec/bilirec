package ds

type void struct{}

var empty void

type Set[T comparable] interface {
	Add(item T) bool
	Remove(item T) bool
	Contains(item T) bool
	Size() int
	ToSlice() []T
	Clear()
}

type AtomicSet[T comparable] interface {
	Set[T]
}

func NewSet[T comparable]() Set[T] {
	return &mapSet[T]{data: make(map[T]void)}
}

func NewSyncedSet[T comparable]() Set[T] {
	return &syncedSet[T]{set: NewSet[T]()}
}
