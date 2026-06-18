package pool

import "testing"

func TestBucketedBytesPool_GetPutHit(t *testing.T) {
	p := NewBucketedBytesPool(512 * 1024)
	buf := p.GetSized(256 * 1024)
	if cap(buf) < 256*1024 {
		t.Fatalf("expected bucket at least 256KB, cap=%d", cap(buf))
	}
	p.Put(buf)

	stats := p.Stats()
	if stats.Hits != 1 {
		t.Fatalf("expected 1 hit, got %d", stats.Hits)
	}
}

func TestBucketedBytesPool_OversizedNotPooled(t *testing.T) {
	p := NewBucketedBytesPool(512 * 1024)
	buf := p.GetSized(6 * 1024 * 1024)
	if len(buf) != 6*1024*1024 {
		t.Fatalf("unexpected oversized len=%d", len(buf))
	}
	p.Put(buf)
	if stats := p.Stats(); stats.Oversized != 1 || stats.Misses != 1 {
		t.Fatalf("expected oversized=1 miss=1, got %+v", stats)
	}
}

func TestBucketedBytesPool_BoundedPerBucket(t *testing.T) {
	p := NewBucketedBytesPool(512*1024,
		WithPoolBoundedMode(true),
		WithPoolBoundedCapacity(1),
	)
	b1 := p.GetSized(512 * 1024)
	b2 := p.GetSized(512 * 1024)
	p.Put(b1)
	p.Put(b2)

	if got := p.GetSized(512 * 1024); got == nil {
		t.Fatal("expected non-nil buffer")
	}
	if got := p.GetSized(512 * 1024); got == nil {
		t.Fatal("expected non-nil second buffer")
	}
}
