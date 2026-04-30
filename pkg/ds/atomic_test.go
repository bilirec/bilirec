package ds

import "testing"

func TestAtomic_LoadStore(t *testing.T) {
	var a Atomic[string]

	if _, ok := a.Load(); ok {
		t.Fatal("expected uninitialized atomic value")
	}

	a.Store("hello")
	got, ok := a.Load()
	if !ok {
		t.Fatal("expected initialized atomic value")
	}
	if got != "hello" {
		t.Fatalf("unexpected value: %q", got)
	}
}

func TestAtomic_LoadOrAndMustLoad(t *testing.T) {
	var a Atomic[int]

	if got := a.LoadOr(42); got != 42 {
		t.Fatalf("expected fallback value 42, got %d", got)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("expected MustLoad to panic on uninitialized value")
		}
	}()
	_ = a.MustLoad()
}

func TestAtomic_Swap(t *testing.T) {
	var a Atomic[int]

	old, loaded := a.Swap(10)
	if loaded {
		t.Fatal("expected first swap to report uninitialized state")
	}
	if old != 0 {
		t.Fatalf("expected zero old value, got %d", old)
	}

	old, loaded = a.Swap(20)
	if !loaded {
		t.Fatal("expected second swap to report initialized state")
	}
	if old != 10 {
		t.Fatalf("expected old value 10, got %d", old)
	}
}

func TestAtomic_CompareAndSwap(t *testing.T) {
	var a Atomic[int]

	if a.CompareAndSwap(0, 1) {
		t.Fatal("expected CAS to fail on uninitialized atomic")
	}

	a.Store(5)
	if !a.CompareAndSwap(5, 8) {
		t.Fatal("expected CAS to succeed")
	}
	if got := a.LoadOr(0); got != 8 {
		t.Fatalf("expected value 8 after CAS, got %d", got)
	}

	if a.CompareAndSwap(5, 9) {
		t.Fatal("expected CAS with stale old value to fail")
	}
}
