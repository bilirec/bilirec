package ds

import "testing"

func TestAtomicSetLoadAndStoreAndDelete(t *testing.T) {
	set := NewAtomicSet[string]()

	if loaded := set.LoadAndStore("task-1"); loaded {
		t.Fatal("first LoadAndStore should report missing item")
	}

	if loaded := set.LoadAndStore("task-1"); !loaded {
		t.Fatal("second LoadAndStore should report existing item")
	}

	if !set.Contains("task-1") {
		t.Fatal("item should exist after LoadAndStore")
	}

	if deleted := set.LoadAndDelete("task-1"); !deleted {
		t.Fatal("LoadAndDelete should remove existing item")
	}

	if deleted := set.LoadAndDelete("task-1"); deleted {
		t.Fatal("LoadAndDelete should report missing item after removal")
	}
}
