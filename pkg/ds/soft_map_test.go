package ds

import "testing"

func TestSoftMap_DeletePreservesValue(t *testing.T) {
	m := NewSoftMap[int, string]()

	m.Store(1, "hello")

	if got, ok := m.Load(1); !ok {
		t.Fatal("expected value to be loaded before delete")
	} else if got != "hello" {
		t.Fatalf("expected value hello, got %q", got)
	}

	m.Delete(1)

	if _, ok := m.Load(1); ok {
		t.Fatal("expected value to be unavailable after soft delete")
	}

	if got, ok := m.LoadStale(1); !ok {
		t.Fatal("expected LoadStale to return soft deleted value")
	} else if got != "hello" {
		t.Fatalf("expected soft deleted value hello, got %q", got)
	}
}

func TestSoftMap_ReviveRestoresValue(t *testing.T) {
	m := NewSoftMap[int, string]()

	m.Store(42, "answer")
	m.Delete(42)

	if revived := m.Revive(42); !revived {
		t.Fatal("expected revive to succeed for soft deleted key")
	}

	if got, ok := m.Load(42); !ok {
		t.Fatal("expected value to be loaded after revive")
	} else if got != "answer" {
		t.Fatalf("expected revived value answer, got %q", got)
	}
}

func TestSoftMap_HardDeleteRemovesValue(t *testing.T) {
	m := NewSoftMap[int, string]()

	m.Store(7, "seven")
	m.Delete(7)
	m.HardDelete(7)

	if _, ok := m.LoadStale(7); ok {
		t.Fatal("expected LoadStale to return false after hard delete")
	}
}
