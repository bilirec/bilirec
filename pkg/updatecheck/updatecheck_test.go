package updatecheck

import (
	"testing"
)
func TestShouldCheck_NoInjectedVersion(t *testing.T) {
	old := currentVersionInjected
	currentVersionInjected = ""
	t.Cleanup(func() { currentVersionInjected = old })

	t.Setenv("CHECK_UPDATE", "true")
	if shouldCheck() {
		t.Fatal("expected shouldCheck false when version not injected")
	}
}

func TestCheckUpdateEnabled(t *testing.T) {
	t.Setenv("CHECK_UPDATE", "true")
	if !checkUpdateEnabled() {
		t.Fatal("expected true when CHECK_UPDATE=true")
	}

	t.Setenv("CHECK_UPDATE", "false")
	if checkUpdateEnabled() {
		t.Fatal("expected false when CHECK_UPDATE=false")
	}

	t.Setenv("CHECK_UPDATE", "")
	if checkUpdateEnabled() {
		t.Fatal("expected false when CHECK_UPDATE is empty string")
	}
}

func TestCached_BeforeCheck(t *testing.T) {
	old := currentVersionInjected
	oldCache := cached
	currentVersionInjected = "v1.0.0"
	cached = Result{}
	t.Cleanup(func() {
		currentVersionInjected = old
		cached = oldCache
	})

	res := Cached()
	if res.Current != "v1.0.0" {
		t.Fatalf("expected current v1.0.0, got %q", res.Current)
	}
	if res.Checked {
		t.Fatal("expected checked false before Check()")
	}
	if res.URL != releasesURL {
		t.Fatalf("expected releases URL, got %q", res.URL)
	}
}

func TestCheck_NoInjectedVersion(t *testing.T) {
	old := currentVersionInjected
	oldCache := cached
	currentVersionInjected = ""
	cached = Result{}
	t.Cleanup(func() {
		currentVersionInjected = old
		cached = oldCache
	})

	res, err := Check()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Current != "" {
		t.Fatalf("expected empty current, got %q", res.Current)
	}
	if res.Checked {
		t.Fatal("expected checked false without injected version")
	}
}

func TestInvokeCheck_SkipsWithoutNetwork(t *testing.T) {
	old := currentVersionInjected
	currentVersionInjected = ""
	t.Cleanup(func() { currentVersionInjected = old })

	t.Setenv("GOLATEST_DISABLE", "1")
	t.Setenv("CHECK_UPDATE", "true")

	InvokeCheck()
}
