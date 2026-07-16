package fp

func Map[T any, R any](items []T, mapper func(T) R) []R {
	if items == nil {
		return nil
	}
	out := make([]R, len(items))
	for i, item := range items {
		out[i] = mapper(item)
	}
	return out
}

func MapErr[T any, R any](items []T, mapper func(T) (R, error)) ([]R, error) {
	if items == nil {
		return nil, nil
	}
	out := make([]R, len(items))
	for i, item := range items {
		mapped, err := mapper(item)
		if err != nil {
			return nil, err
		}
		out[i] = mapped
	}
	return out, nil
}

func Filter[T any](items []T, predicate func(T) bool) []T {
	if items == nil {
		return nil
	}
	out := make([]T, 0, len(items))
	for _, item := range items {
		if predicate(item) {
			out = append(out, item)
		}
	}
	return out
}

// Unique returns items with duplicates removed, preserving first-seen order.
func Unique[T comparable](items []T) []T {
	if items == nil {
		return nil
	}
	seen := make(map[T]struct{}, len(items))
	out := make([]T, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func FlatMap[T any, R any](items []T, mapper func(T) []R) []R {
	if items == nil {
		return nil
	}
	out := make([]R, 0)
	for _, item := range items {
		out = append(out, mapper(item)...)
	}
	return out
}

func Reduce[T any, R any](items []T, initial R, reducer func(R, T) R) R {
	acc := initial
	for _, item := range items {
		acc = reducer(acc, item)
	}
	return acc
}

func Any[T any](items []T, predicate func(T) bool) bool {
	for _, item := range items {
		if predicate(item) {
			return true
		}
	}
	return false
}

func All[T any](items []T, predicate func(T) bool) bool {
	for _, item := range items {
		if !predicate(item) {
			return false
		}
	}
	return true
}

func Find[T any](items []T, predicate func(T) bool) (T, bool) {
	for _, item := range items {
		if predicate(item) {
			return item, true
		}
	}
	var zero T
	return zero, false
}
