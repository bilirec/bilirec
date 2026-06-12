package pool

// ByteSlicePool defines a shared contract for variable-sized byte slice pools.
// Implementations may decide their own pooling strategy (single-cap, bucketed,
// bounded, etc.) as long as they can provide a sized buffer and accept a put-back.
type ByteSlicePool interface {
	GetSized(size int) []byte
	Put(buf []byte)
}

func normalizeByteSliceSize(size int) int {
	if size < 0 {
		return 0
	}
	return size
}

