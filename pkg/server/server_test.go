package server

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/haproxy-metrics-spoa/pkg/metrics"
	"github.com/haproxy-metrics-spoa/pkg/normalizer"
	"github.com/haproxy-metrics-spoa/pkg/spoa"
	"github.com/prometheus/client_golang/prometheus"
)

func newTestDependencies() (*spoa.Handler, *prometheus.Registry) {
	guard := normalizer.NewGuard(normalizer.Config{MaxTrackedHosts: 10, GroupIPs: true})
	reg := prometheus.NewRegistry()
	col := metrics.NewCollector(guard, reg)
	h := spoa.NewHandler(col)
	return h, reg
}

func TestServerStartAndScrapeMetrics(t *testing.T) {
	tempDir := t.TempDir()
	sockPath := filepath.Join(tempDir, "s.sock")

	h, reg := newTestDependencies()

	cfg := Config{
		SPOEListen:    "unix://" + sockPath,
		SocketMode:    0660,
		MetricsListen: "127.0.0.1:0", // random free port
		MetricsPath:   "/metrics",
	}

	srv, err := NewServer(cfg, h, reg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	if srv.MetricsAddr() != "" {
		t.Fatalf("expected empty metrics addr before start, got %q", srv.MetricsAddr())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	// Wait for listener to be ready
	time.Sleep(100 * time.Millisecond)

	select {
	case err := <-errCh:
		t.Fatalf("srv.Start failed: %v", err)
	default:
	}

	// Check if UNIX socket was created
	info, err := os.Stat(sockPath)
	if os.IsNotExist(err) {
		t.Fatalf("expected unix socket file at %s", sockPath)
	}
	if info.Mode().Perm() != 0660 {
		t.Fatalf("expected socket mode 0660, got %v", info.Mode().Perm())
	}

	// Scrape metrics HTTP server
	resp, err := http.Get("http://" + srv.MetricsAddr() + "/metrics")
	if err != nil {
		t.Fatalf("failed to scrape metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "haproxy_spoa_tracked_hosts_total") {
		t.Fatalf("missing tracked hosts metric in response")
	}

	// Test /healthz
	healthResp, err := http.Get("http://" + srv.MetricsAddr() + "/healthz")
	if err != nil {
		t.Fatalf("failed to check healthz: %v", err)
	}
	defer healthResp.Body.Close()

	if healthResp.StatusCode != http.StatusOK {
		t.Fatalf("expected healthz status 200, got %d", healthResp.StatusCode)
	}
	healthBody, _ := io.ReadAll(healthResp.Body)
	if string(healthBody) != "OK\n" {
		t.Fatalf("expected 'OK\\n', got %q", string(healthBody))
	}

	// Shutdown
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}

	// Socket file should be unlinked
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Fatalf("expected unix socket file to be removed after shutdown")
	}
}

func TestServerTCPListener(t *testing.T) {
	h, reg := newTestDependencies()

	cfg := Config{
		SPOEListen:    "tcp://127.0.0.1:0",
		MetricsListen: "127.0.0.1:0",
		MetricsPath:   "/metrics",
	}

	srv, err := NewServer(cfg, h, reg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	select {
	case err := <-errCh:
		t.Fatalf("srv.Start failed: %v", err)
	default:
	}

	resp, err := http.Get("http://" + srv.MetricsAddr() + "/metrics")
	if err != nil {
		t.Fatalf("failed to scrape metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}
}

func TestServerValidationAndErrors(t *testing.T) {
	h, reg := newTestDependencies()
	cfg := Config{SPOEListen: "127.0.0.1:0", MetricsListen: "127.0.0.1:0"}

	if _, err := NewServer(cfg, nil, reg); err == nil {
		t.Fatal("expected error with nil handler")
	}
	if _, err := NewServer(cfg, h, nil); err == nil {
		t.Fatal("expected error with nil registry")
	}

	// Unsupported scheme
	invalidCfg := Config{
		SPOEListen:    "ftp://127.0.0.1:9000",
		MetricsListen: "127.0.0.1:0",
	}
	srv, err := NewServer(invalidCfg, h, reg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	if err := srv.Start(context.Background()); err == nil {
		t.Fatal("expected error starting server with unsupported scheme")
	}

	// Double start
	validCfg := Config{
		SPOEListen:    "tcp://127.0.0.1:0",
		MetricsListen: "127.0.0.1:0",
	}
	srvValid, err := NewServer(validCfg, h, reg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = srvValid.Start(ctx)
	}()
	time.Sleep(50 * time.Millisecond)
	if err := srvValid.Start(ctx); err == nil {
		t.Fatal("expected error on second call to Start")
	}
	_ = srvValid.Shutdown(context.Background())
}

func TestServerDefaultScheme(t *testing.T) {
	tempDir := t.TempDir()
	sockPath := filepath.Join(tempDir, "default.sock")

	h, reg := newTestDependencies()

	// Omit "unix://" prefix to verify automatic unix scheme defaulting
	cfg := Config{
		SPOEListen:    sockPath,
		MetricsListen: "127.0.0.1:0",
	}

	srv, err := NewServer(cfg, h, reg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	select {
	case err := <-errCh:
		t.Fatalf("srv.Start failed: %v", err)
	default:
	}

	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		t.Fatalf("expected unix socket file at %s", sockPath)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}
}
