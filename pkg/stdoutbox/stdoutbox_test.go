package stdoutbox_test

import (
	"strings"
	"testing"

	"github.com/bilirec/bilirec/pkg/stdoutbox"
)

func TestPrintTo(t *testing.T) {
	var buf strings.Builder
	stdoutbox.PrintTo(&buf,
		"新版本可用!",
		"当前版本:  v1.0.0",
		"最新版本:  v1.2.3",
	)

	out := buf.String()
	t.Log(out)
	for _, want := range []string{
		"新版本可用!",
		"当前版本:  v1.0.0",
		"最新版本:  v1.2.3",
		"+",
		"|",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}
