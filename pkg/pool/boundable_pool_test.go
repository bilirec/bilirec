package pool

import "testing"

func TestBoundablePool_BoundedDropsWhenFull(t *testing.T) {
	slot := newBoundablePool(
		PoolBoundedConfig{Mode: BufferPoolModeBounded, Capacity: 1},
		func() []byte { return make([]byte, 8) },
		func(b []byte) []byte { return b },
		func(b []byte) ([]byte, bool) { return b, true },
	)

	b1 := slot.get()
	b2 := slot.get()
	if b1 == nil || b2 == nil {
		t.Fatal("expected non-nil buffers")
	}

	slot.put(b1)
	slot.put(b2)

	g1 := slot.get()
	if g1 == nil {
		t.Fatal("expected non-nil buffer from bounded pool")
	}
	g2 := slot.get()
	if g2 == nil {
		t.Fatal("expected non-nil second buffer even when queue empty")
	}
}

func TestBoundablePool_SoftModeReuses(t *testing.T) {
	var created int
	slot := newBoundablePool(
		PoolBoundedConfig{Mode: BufferPoolModeSoft},
		func() []byte {
			created++
			return make([]byte, 8)
		},
		func(b []byte) []byte { return b },
		func(b []byte) ([]byte, bool) { return b, true },
	)

	buf := slot.get()
	slot.put(buf)
	_ = slot.get()
	if created != 1 {
		t.Fatalf("expected one allocation in soft mode, got %d", created)
	}
}

func TestBoundablePool_ReclaimRejects(t *testing.T) {
	slot := newBoundablePool(
		PoolBoundedConfig{Mode: BufferPoolModeBounded, Capacity: 2},
		func() []byte { return make([]byte, 8) },
		func(b []byte) []byte { return b },
		func(b []byte) ([]byte, bool) {
			return b, false
		},
	)
	slot.put(make([]byte, 8))
	got := slot.get()
	if got == nil {
		t.Fatal("expected get to allocate when reclaim rejects")
	}
}
