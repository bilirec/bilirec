package pool

import "testing"

func TestBytesPool_SoftModeDefault(t *testing.T) {
	p := NewBytesPool(512 * 1024)
	buf := p.GetBytes()
	if len(buf) != 512*1024 || cap(buf) != 512*1024 {
		t.Fatalf("unexpected buffer size: len=%d cap=%d", len(buf), cap(buf))
	}
	p.PutBytes(buf)

	buf2 := p.GetBytes()
	if cap(buf2) != 512*1024 {
		t.Fatalf("expected cap %d, got %d", 512*1024, cap(buf2))
	}
}

func TestBytesPool_BoundedDropsWhenFull(t *testing.T) {
	p := NewBytesPool(1024,
		WithPoolBoundedMode(true),
		WithPoolBoundedCapacity(1),
	)

	b1 := p.GetBytes()
	b2 := p.GetBytes()
	p.PutBytes(b1)
	p.PutBytes(b2)

	if got := p.GetBytes(); got == nil {
		t.Fatal("expected non-nil buffer")
	}
	if got := p.GetBytes(); got == nil {
		t.Fatal("expected non-nil second buffer")
	}
}

func TestBytesPool_PutRejectsWrongCap(t *testing.T) {
	p := NewBytesPool(1024,
		WithPoolBoundedMode(true),
		WithPoolBoundedCapacity(2),
	)
	p.PutBytes(make([]byte, 512))
	if got := p.GetBytes(); len(got) != 1024 {
		t.Fatalf("expected fresh allocation, got len=%d", len(got))
	}
}

func TestBytesPool_GetSized(t *testing.T) {
	p := NewBytesPool(1024)

	small := p.GetSized(128)
	if len(small) != 128 || cap(small) != 1024 {
		t.Fatalf("unexpected small buffer: len=%d cap=%d", len(small), cap(small))
	}
	p.Put(small)

	large := p.GetSized(2048)
	if len(large) != 2048 || cap(large) != 2048 {
		t.Fatalf("unexpected oversized buffer: len=%d cap=%d", len(large), cap(large))
	}
	p.Put(large)
}
