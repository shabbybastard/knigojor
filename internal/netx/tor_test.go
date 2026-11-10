package netx

import (
	"context"
	"net"
	"testing"
)

func TestTorManagerBasics(t *testing.T) {
	manager := NewTorManager("/nonexistent/tor", "127.0.0.1", 9050, 9051, t.TempDir(), true)
	if manager.IsInstalled() {
		t.Fatal("nonexistent tor reported as installed")
	}
	if got := manager.ProxyURL(); got != "socks5://127.0.0.1:9050" {
		t.Fatalf("unexpected proxy URL: %s", got)
	}
	if manager.Start(context.Background()) {
		t.Fatal("tor started without an executable")
	}

	disabled := NewTorManager("/nonexistent/tor", "127.0.0.1", 9050, 9051, t.TempDir(), false)
	if disabled.Start(context.Background()) {
		t.Fatal("disabled tor started")
	}
}

func TestTorProbe(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	manager := NewTorManager("/nonexistent/tor", "127.0.0.1", port, 0, t.TempDir(), true)
	if !manager.Probe(context.Background()) {
		t.Fatal("open listener was not detected")
	}
	_ = listener.Close()
	if manager.Probe(context.Background()) {
		t.Fatal("closed listener was detected")
	}
}
