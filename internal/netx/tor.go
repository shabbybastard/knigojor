package netx

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// TorManager starts and probes a private Tor daemon.
type TorManager struct {
	Executable  string
	SocksHost   string
	SocksPort   int
	ControlPort int
	StateDir    string
	Enabled     bool

	process *os.Process
	waitCh  chan error
	logFile *os.File
}

// NewTorManager creates a Tor manager.
func NewTorManager(executable, socksHost string, socksPort, controlPort int, stateDir string, enabled bool) *TorManager {
	return &TorManager{
		Executable:  executable,
		SocksHost:   socksHost,
		SocksPort:   socksPort,
		ControlPort: controlPort,
		StateDir:    stateDir,
		Enabled:     enabled,
	}
}

// IsInstalled reports whether the tor executable exists.
func (t *TorManager) IsInstalled() bool {
	if _, err := exec.LookPath(t.Executable); err == nil {
		return true
	}
	_, err := os.Stat(t.Executable)
	return err == nil
}

// IsRunning reports whether the child process is alive.
func (t *TorManager) IsRunning() bool {
	return t.process != nil
}

// ProxyURL returns the SOCKS5 URL for HTTP clients.
func (t *TorManager) ProxyURL() string {
	return fmt.Sprintf("socks5://%s:%d", t.SocksHost, t.SocksPort)
}

// Start launches Tor and waits until its SOCKS port accepts connections.
func (t *TorManager) Start(ctx context.Context) bool {
	if !t.Enabled {
		return false
	}
	if !t.IsInstalled() {
		slog.Warn("Tor executable not found", "executable", t.Executable)
		return false
	}
	if t.Probe(ctx) {
		slog.Info("Tor already running", "address", fmt.Sprintf("%s:%d", t.SocksHost, t.SocksPort))
		return true
	}

	if err := os.MkdirAll(t.StateDir, 0o700); err != nil {
		slog.Error("failed to create Tor state dir", "error", err)
		return false
	}
	_ = os.Chmod(t.StateDir, 0o700)
	dataDir := filepath.Join(t.StateDir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		slog.Error("failed to create Tor data dir", "error", err)
		return false
	}
	_ = os.Chmod(dataDir, 0o700)

	torrc := filepath.Join(t.StateDir, "torrc")
	config := fmt.Sprintf(
		"SocksPort %s:%d\nControlPort %d\nDataDirectory %s\nCookieAuthentication 1\nLog notice stdout\n",
		t.SocksHost, t.SocksPort, t.ControlPort, dataDir,
	)
	if err := os.WriteFile(torrc, []byte(config), 0o600); err != nil {
		slog.Error("failed to write torrc", "error", err)
		return false
	}

	logFile, err := os.OpenFile(filepath.Join(t.StateDir, "tor.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		slog.Error("failed to open Tor log", "error", err)
		return false
	}
	t.logFile = logFile

	cmd := exec.Command(t.Executable, "-f", torrc)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		slog.Error("failed to start Tor", "error", err)
		_ = logFile.Close()
		t.logFile = nil
		return false
	}
	t.process = cmd.Process
	t.waitCh = make(chan error, 1)
	go func() {
		t.waitCh <- cmd.Wait()
	}()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			t.Stop()
			return false
		case <-time.After(time.Second):
		}
		if t.Probe(ctx) {
			slog.Info("Tor started")
			return true
		}
	}
	slog.Error("Tor did not become ready")
	t.Stop()
	return false
}

// Probe checks that the SOCKS port accepts a TCP connection.
func (t *TorManager) Probe(ctx context.Context) bool {
	dialer := net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", t.SocksHost, t.SocksPort))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Stop terminates the child process.
func (t *TorManager) Stop() {
	if t.process != nil {
		_ = t.process.Signal(syscall.SIGTERM)
		if t.waitCh != nil {
			select {
			case <-t.waitCh:
			case <-time.After(10 * time.Second):
				_ = t.process.Kill()
				select {
				case <-t.waitCh:
				case <-time.After(5 * time.Second):
				}
			}
		}
		t.process = nil
		t.waitCh = nil
	}
	if t.logFile != nil {
		_ = t.logFile.Close()
		t.logFile = nil
	}
}
