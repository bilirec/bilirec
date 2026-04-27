package pool

import "testing"

func TestBufferPoolSoftModeDefault(t *testing.T) {
	bp := NewBufferPool(16, 64)

	buf := bp.Get()
	if buf == nil {
		t.Fatal("expected non-nil buffer")
	}
	buf.WriteString("abc")
	bp.Put(buf)

	buf2 := bp.Get()
	if buf2 == nil {
		t.Fatal("expected non-nil buffer on second get")
	}
	if buf2.Len() != 0 {
		t.Fatalf("expected reset buffer len 0, got %d", buf2.Len())
	}
}

func TestBufferPoolBoundedDropsWhenFull(t *testing.T) {
	bp := NewBufferPool(
		16,
		64,
		WithBoundedMode(true),
		WithBoundedCapacity(1),
	)

	b1 := bp.Get()
	b2 := bp.Get()
	if b1 == nil || b2 == nil {
		t.Fatal("expected non-nil buffers")
	}

	bp.Put(b1)
	bp.Put(b2) // should be dropped because bounded queue is full

	g1 := bp.Get()
	if g1 == nil {
		t.Fatal("expected non-nil buffer from bounded pool")
	}
	// Queue capacity is 1, so second Get must allocate/new and still be non-nil.
	g2 := bp.Get()
	if g2 == nil {
		t.Fatal("expected non-nil second buffer even when queue empty")
	}
}
