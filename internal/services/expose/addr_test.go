package expose

import (
	"strings"
	"testing"
)

// --- parseServerAddr (whitebox) ---

func TestParseServerAddr_HostPort(t *testing.T) {
	host, port, err := parseServerAddr("frp.example.com:7000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "frp.example.com" {
		t.Errorf("host: got %q, want %q", host, "frp.example.com")
	}
	if port != 7000 {
		t.Errorf("port: got %d, want %d", port, 7000)
	}
}

func TestParseServerAddr_HostOnly(t *testing.T) {
	host, port, err := parseServerAddr("frp.example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "frp.example.com" {
		t.Errorf("host: got %q, want %q", host, "frp.example.com")
	}
	if port != defaultFRPServerPort {
		t.Errorf("port: got %d, want default %d", port, defaultFRPServerPort)
	}
}

func TestParseServerAddr_IPv4WithPort(t *testing.T) {
	host, port, err := parseServerAddr("192.168.1.1:7001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "192.168.1.1" {
		t.Errorf("host: got %q, want %q", host, "192.168.1.1")
	}
	if port != 7001 {
		t.Errorf("port: got %d, want %d", port, 7001)
	}
}

func TestParseServerAddr_IPv6HostOnlyWithoutBrackets(t *testing.T) {
	host, port, err := parseServerAddr("::1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "::1" {
		t.Errorf("host: got %q, want %q", host, "::1")
	}
	if port != defaultFRPServerPort {
		t.Errorf("port: got %d, want default %d", port, defaultFRPServerPort)
	}
}

func TestParseServerAddr_IPv6WithPort(t *testing.T) {
	host, port, err := parseServerAddr("[::1]:7002")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "::1" {
		t.Errorf("host: got %q, want %q", host, "::1")
	}
	if port != 7002 {
		t.Errorf("port: got %d, want %d", port, 7002)
	}
}

func TestParseServerAddr_Empty(t *testing.T) {
	_, _, err := parseServerAddr("")
	if err == nil {
		t.Fatal("expected error for empty addr, got nil")
	}
}

func TestParseServerAddr_InvalidPort(t *testing.T) {
	_, _, err := parseServerAddr("frp.example.com:abc")
	if err == nil {
		t.Fatal("expected error for non-numeric port, got nil")
	}
}

func TestParseServerAddr_ColonOnly(t *testing.T) {
	_, _, err := parseServerAddr(":")
	if err == nil {
		t.Fatal("expected error for addr ':', got nil")
	}
}

func TestParseServerAddr_MalformedIPv6Bracket(t *testing.T) {
	_, _, err := parseServerAddr("[::1")
	if err == nil {
		t.Fatal("expected error for malformed bracketed IPv6, got nil")
	}
}

func TestParseServerAddr_ExtraColonSegments(t *testing.T) {
	_, _, err := parseServerAddr("frp.example.com:7000:bad")
	if err == nil {
		t.Fatal("expected error for malformed addr with extra colon segments, got nil")
	}
	if !strings.Contains(err.Error(), "无效的 FRP_SERVER 格式") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseServerAddr_WithScheme(t *testing.T) {
	_, _, err := parseServerAddr("http://frp.example.com:7000")
	if err == nil {
		t.Fatal("expected error for addr containing scheme, got nil")
	}
	if !strings.Contains(err.Error(), "无效的 FRP_SERVER 格式") {
		t.Fatalf("unexpected error: %v", err)
	}
}
