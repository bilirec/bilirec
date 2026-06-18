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
