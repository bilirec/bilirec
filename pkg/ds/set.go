package ds

type void struct{}

var empty void

type Set[T comparable] interface {
	Add(item T)
	Remove(item T)
	Contains(item T) bool
	Size() int
	ToSlice() []T
	Clear()
}

type AtomicSet[T comparable] interface {
	Set[T]
	LoadAndStore(item T) bool
	LoadAndDelete(item T) bool
}

func NewSet[T comparable]() Set[T] {
	return &mapSet[T]{data: make(map[T]void)}
}

func NewSyncedSet[T comparable]() Set[T] {
	return &syncedSet[T]{set: NewSet[T]()}
}

func NewAtomicSet[T comparable]() AtomicSet[T] {
	return &syncedSet[T]{set: NewSet[T]()}
}
