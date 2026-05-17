package expose

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/fatedier/frp/client"
	"github.com/fatedier/frp/client/proxy"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("captureStdout: failed to create pipe: %v", err)
	}

	old := os.Stdout
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	_ = w.Close()
	os.Stdout = old
	result := <-done
	_ = r.Close()
	return result
}

func TestPrintTunnelBox_KeepsRightPadding(t *testing.T) {
	output := captureStdout(t, func() {
		printTunnelBox("127.0.0.1", "https://e3e60284b51940578bd10f93.tunnel.bilirec.org")
	})

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 6 {
		t.Fatalf("expected at least 6 box lines, got %d in output:\n%s", len(lines), output)
	}

	remoteLine := lines[4]
	if !strings.HasSuffix(remoteLine, " |") {
		t.Fatalf("expected remote line to keep one space before right border, got %q", remoteLine)
	}
	if !strings.Contains(remoteLine, "Remote Public:  https://e3e60284b51940578bd10f93.tunnel.bilirec.org") {
		t.Fatalf("expected remote line to include public URL, got %q", remoteLine)
	}
	if strings.Contains(output, "\n2026-") {
		t.Fatalf("expected pure box output without interleaved logs, got:\n%s", output)
	}
}

type fakeTunnelService struct {
	exporter client.StatusExporter
}

func (f *fakeTunnelService) Run(context.Context) error {
	return nil
}

func (f *fakeTunnelService) StatusExporter() client.StatusExporter {
	return f.exporter
}

type stagedStatusExporter struct {
}

func (s *stagedStatusExporter) GetProxyStatus(name string) (*proxy.WorkingStatus, bool) {
	return &proxy.WorkingStatus{Name: name, Phase: proxy.ProxyPhaseRunning, RemoteAddr: "https://deadbeef.example.test"}, true
}

func TestWaitAndPrintTunnelBox_PrintsAfterRunningStatus(t *testing.T) {
	svc := &Service{
		svc: &fakeTunnelService{exporter: &stagedStatusExporter{}},
		ctx: context.Background(),
	}

	output := captureStdout(t, func() {
		svc.waitAndPrintTunnelBox("127.0.0.1:8080", "bilirec-deadbeef", "https://deadbeef.example.test")
	})

	if !strings.Contains(output, "Tunnel is established!") {
		t.Fatalf("expected tunnel box output, got:\n%s", output)
	}
	if !strings.Contains(output, "Remote Public:  https://deadbeef.example.test") {
		t.Fatalf("expected remote URL in tunnel box output, got:\n%s", output)
	}
}
