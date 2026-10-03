# HAProxy Host-Header Metrics SPOA Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `haproxy-metrics-spoa`, a high-throughput Go daemon that consumes HAProxy SPOE transaction events, extracts and normalizes the HTTP `Host` header with cardinality protection, and exposes Prometheus RED metrics per virtual host.

**Architecture:** An SPOP protocol agent using `github.com/dropmorepackets/haproxy-go` listens over UNIX domain socket or TCP, receives `on-http-response` NOTIFY messages containing HTTP transaction metadata, normalizes hostnames through a thread-safe cardinality guard, and updates atomic Prometheus counters and latency histograms served on an HTTP endpoint (`:9101/metrics`).

**Tech Stack:** Go 1.22+, `github.com/dropmorepackets/haproxy-go`, `github.com/prometheus/client_golang`, `net/netip`.

**Spec:** [docs/superpowers/specs/2026-10-03-haproxy-host-metrics-spoa-design.md](file:///Users/mrpk1906/Workspace/haproxy-metrics-spoa/docs/superpowers/specs/2026-10-03-haproxy-host-metrics-spoa-design.md)

## Global Constraints
- Target Go version: Go 1.22+
- Library for SPOP protocol: `github.com/dropmorepackets/haproxy-go`
- Prometheus client library: `github.com/prometheus/client_golang`
- Transport options: UNIX domain socket (`unix:///path/to/sock`) and TCP (`tcp://host:port`)
- SPOP Message name: `http-response-metric`
- Default metrics port: `:9101`, default path: `/metrics`
- Zero external CGo dependencies; fully statically compilable Go binary.

---

### Task 1: Project Scaffolding & Dependencies

**Files:**
- Create: `go.mod`
- Create: `internal/version/version.go`
- Test: `internal/version/version_test.go`

**Interfaces:**
- Produces: `version.Version` (string), `version.Info()` (string)

- [ ] **Step 1: Create go.mod**

```go
module github.com/mrpk1906/haproxy-metrics-spoa

go 1.22.0

require (
	github.com/dropmorepackets/haproxy-go v0.1.1
	github.com/prometheus/client_golang v1.19.0
)
```

- [ ] **Step 2: Write failing test for version info**

Create `internal/version/version_test.go`:
```go
package version

import (
	"testing"
)

func TestVersionInfo(t *testing.T) {
	Version = "0.1.0"
	expected := "haproxy-metrics-spoa 0.1.0"
	if got := Info(); got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/version`  
Expected: FAIL (undefined: Version, Info)

- [ ] **Step 4: Implement version.go**

Create `internal/version/version.go`:
```go
package version

import "fmt"

var Version = "dev"

func Info() string {
	return fmt.Sprintf("haproxy-metrics-spoa %s", Version)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/version`  
Expected: PASS

---

### Task 2: Host Normalizer & Cardinality Guard

**Files:**
- Create: `pkg/normalizer/normalizer.go`
- Test: `pkg/normalizer/normalizer_test.go`

**Interfaces:**
- Consumes: Standard library (`net/netip`, `strings`, `sync`)
- Produces: `type Config struct { MaxTrackedHosts int; GroupIPs bool }`, `type Guard struct`, `NewGuard(cfg Config) *Guard`, `(g *Guard) Normalize(rawHost string) string`, `(g *Guard) TrackedCount() int`

- [ ] **Step 1: Write failing tests for Normalizer & Cardinality Guard**

Create `pkg/normalizer/normalizer_test.go`:
```go
package normalizer

import (
	"fmt"
	"testing"
)

func TestNormalizeHostnames(t *testing.T) {
	guard := NewGuard(Config{
		MaxTrackedHosts: 100,
		GroupIPs:        true,
	})

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"clean domain", "example.com", "example.com"},
		{"port stripping", "example.com:8443", "example.com"},
		{"casing and spaces", "  ExAmPlE.CoM  ", "example.com"},
		{"trailing dot", "example.com.", "example.com"},
		{"ipv4 grouping", "192.168.1.1", "_ip_"},
		{"ipv4 with port", "10.0.0.1:8080", "_ip_"},
		{"ipv6 grouping", "[2001:db8::1]", "_ip_"},
		{"ipv6 raw", "2001:db8::1", "_ip_"},
		{"empty string", "", "_other_"},
		{"malformed host with spaces inside", "example .com", "_other_"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := guard.Normalize(tc.input)
			if got != tc.expected {
				t.Errorf("Normalize(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestCardinalityOverflow(t *testing.T) {
	maxHosts := 5
	guard := NewGuard(Config{
		MaxTrackedHosts: maxHosts,
		GroupIPs:        true,
	})

	// Add 5 distinct hosts
	for i := 0; i < maxHosts; i++ {
		host := fmt.Sprintf("site%d.example.com", i)
		got := guard.Normalize(host)
		if got != host {
			t.Fatalf("expected host %q, got %q", host, got)
		}
	}

	if count := guard.TrackedCount(); count != maxHosts {
		t.Fatalf("expected %d tracked hosts, got %d", maxHosts, count)
	}

	// 6th new host must be mapped to _overflow_
	gotOverflow := guard.Normalize("site999.example.com")
	if gotOverflow != "_overflow_" {
		t.Fatalf("expected '_overflow_', got %q", gotOverflow)
	}

	// Existing host must still resolve to itself
	if gotExisting := guard.Normalize("site0.example.com"); gotExisting != "site0.example.com" {
		t.Fatalf("expected 'site0.example.com', got %q", gotExisting)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/normalizer`  
Expected: FAIL (undefined: NewGuard, Config)

- [ ] **Step 3: Implement pkg/normalizer/normalizer.go**

Create `pkg/normalizer/normalizer.go`:
```go
package normalizer

import (
	"net"
	"net/netip"
	"strings"
	"sync"
)

type Config struct {
	MaxTrackedHosts int
	GroupIPs        bool
}

type Guard struct {
	cfg          Config
	mu           sync.RWMutex
	trackedHosts map[string]struct{}
}

func NewGuard(cfg Config) *Guard {
	if cfg.MaxTrackedHosts <= 0 {
		cfg.MaxTrackedHosts = 5000
	}
	return &Guard{
		cfg:          cfg,
		trackedHosts: make(map[string]struct{}),
	}
}

func (g *Guard) Normalize(rawHost string) string {
	cleaned := strings.TrimSpace(rawHost)
	if cleaned == "" {
		return "_other_"
	}

	// Strip IPv6 brackets if present with or without port: [::1]:80 or [::1]
	if strings.HasPrefix(cleaned, "[") {
		if closeIdx := strings.LastIndex(cleaned, "]"); closeIdx != -1 {
			hostPart := cleaned[1:closeIdx]
			cleaned = hostPart
		}
	} else if host, _, err := net.SplitHostPort(cleaned); err == nil {
		cleaned = host
	}

	cleaned = strings.ToLower(strings.TrimSuffix(cleaned, "."))

	if strings.ContainsAny(cleaned, " \t\r\n/\\") {
		return "_other_"
	}

	if g.cfg.GroupIPs {
		if _, err := netip.ParseAddr(cleaned); err == nil {
			return "_ip_"
		}
	}

	// Check if already tracked
	g.mu.RLock()
	_, exists := g.trackedHosts[cleaned]
	g.mu.RUnlock()
	if exists {
		return cleaned
	}

	// Register new host under write lock
	g.mu.Lock()
	defer g.mu.Unlock()

	// Double-check under write lock
	if _, exists := g.trackedHosts[cleaned]; exists {
		return cleaned
	}

	if len(g.trackedHosts) >= g.cfg.MaxTrackedHosts {
		return "_overflow_"
	}

	g.trackedHosts[cleaned] = struct{}{}
	return cleaned
}

func (g *Guard) TrackedCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.trackedHosts)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./pkg/normalizer`  
Expected: PASS

---

### Task 3: Prometheus Metrics Registry & Collector

**Files:**
- Create: `pkg/metrics/collector.go`
- Test: `pkg/metrics/collector_test.go`

**Interfaces:**
- Consumes: `pkg/normalizer.Guard`, `github.com/prometheus/client_golang/prometheus`
- Produces:
  - `type HTTPMetricEvent struct { Host string; Method string; Status int64; Latency int64; ReqBytes int64; ResBytes int64 }`
  - `type Collector struct`
  - `NewCollector(guard *normalizer.Guard, reg prometheus.Registerer) *Collector`
  - `(c *Collector) RecordEvent(evt HTTPMetricEvent)`
  - `(c *Collector) RecordMessage(status string)`

- [ ] **Step 1: Write failing test for Metrics Collector**

Create `pkg/metrics/collector_test.go`:
```go
package metrics

import (
	"strconv"
	"testing"

	"github.com/mrpk1906/haproxy-metrics-spoa/pkg/normalizer"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestCollectorRecordEvent(t *testing.T) {
	guard := normalizer.NewGuard(normalizer.Config{MaxTrackedHosts: 10, GroupIPs: true})
	reg := prometheus.NewRegistry()
	c := NewCollector(guard, reg)

	c.RecordEvent(HTTPMetricEvent{
		Host:     "api.example.com:443",
		Method:   "GET",
		Status:   200,
		Latency:  125, // 125ms -> 0.125s
		ReqBytes: 512,
		ResBytes: 2048,
	})

	c.RecordMessage("ok")

	metricFamilies, err := reg.Gather()
	if err != nil {
		t.Fatalf("unexpected error gathering metrics: %v", err)
	}

	foundRequests := false
	for _, mf := range metricFamilies {
		if mf.GetName() == "haproxy_host_http_requests_total" {
			foundRequests = true
			if len(mf.Metric) != 1 {
				t.Fatalf("expected 1 metric, got %d", len(mf.Metric))
			}
			m := mf.Metric[0]
			var hostLabel, codeLabel string
			for _, p := range m.Label {
				if p.GetName() == "host" {
					hostLabel = p.GetValue()
				}
				if p.GetName() == "code" {
					codeLabel = p.GetValue()
				}
			}
			if hostLabel != "api.example.com" {
				t.Errorf("expected host 'api.example.com', got %q", hostLabel)
			}
			if codeLabel != "200" {
				t.Errorf("expected code '200', got %q", codeLabel)
			}
			if m.GetCounter().GetValue() != 1 {
				t.Errorf("expected count 1, got %f", m.GetCounter().GetValue())
			}
		}
	}

	if !foundRequests {
		t.Fatal("haproxy_host_http_requests_total metric family not found")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/metrics`  
Expected: FAIL (undefined: HTTPMetricEvent, NewCollector)

- [ ] **Step 3: Implement pkg/metrics/collector.go**

Create `pkg/metrics/collector.go`:
```go
package metrics

import (
	"strconv"

	"github.com/mrpk1906/haproxy-metrics-spoa/pkg/normalizer"
	"github.com/prometheus/client_golang/prometheus"
)

type HTTPMetricEvent struct {
	Host     string `spoe:"host"`
	Method   string `spoe:"method"`
	Status   int64  `spoe:"status"`
	Latency  int64  `spoe:"lat"`
	ReqBytes int64  `spoe:"req_bytes"`
	ResBytes int64  `spoe:"res_bytes"`
}

type Collector struct {
	guard          *normalizer.Guard
	requestsTotal  *prometheus.CounterVec
	durationHist   *prometheus.HistogramVec
	reqBytesTotal  *prometheus.CounterVec
	resBytesTotal  *prometheus.CounterVec
	messagesTotal  *prometheus.CounterVec
	trackedHosts   prometheus.GaugeFunc
}

func NewCollector(guard *normalizer.Guard, reg prometheus.Registerer) *Collector {
	c := &Collector{
		guard: guard,
		requestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "haproxy_host_http_requests_total",
				Help: "Total number of HTTP requests partitioned by host, response code, and method.",
			},
			[]string{"host", "code", "method"},
		),
		durationHist: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "haproxy_host_http_request_duration_seconds",
				Help:    "HTTP request latency distribution partitioned by host.",
				Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
			},
			[]string{"host"},
		),
		reqBytesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "haproxy_host_http_request_bytes_total",
				Help: "Total HTTP request payload bytes received partitioned by host.",
			},
			[]string{"host"},
		),
		resBytesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "haproxy_host_http_response_bytes_total",
				Help: "Total HTTP response payload bytes sent partitioned by host.",
			},
			[]string{"host"},
		),
		messagesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "haproxy_spoa_messages_received_total",
				Help: "Total number of SPOP messages processed by daemon status.",
			},
			[]string{"status"},
		),
		trackedHosts: prometheus.NewGaugeFunc(
			prometheus.GaugeOpts{
				Name: "haproxy_spoa_tracked_hosts_total",
				Help: "Current number of unique virtual hosts actively tracked.",
			},
			func() float64 {
				return float64(guard.TrackedCount())
			},
		),
	}

	reg.MustRegister(
		c.requestsTotal,
		c.durationHist,
		c.reqBytesTotal,
		c.resBytesTotal,
		c.messagesTotal,
		c.trackedHosts,
	)

	return c
}

func (c *Collector) RecordEvent(evt HTTPMetricEvent) {
	normHost := c.guard.Normalize(evt.Host)
	codeStr := strconv.FormatInt(evt.Status, 10)
	method := evt.Method
	if method == "" {
		method = "UNKNOWN"
	}

	c.requestsTotal.WithLabelValues(normHost, codeStr, method).Inc()

	if evt.Latency >= 0 {
		// latency from HAProxy is in milliseconds -> convert to seconds
		durationSec := float64(evt.Latency) / 1000.0
		c.durationHist.WithLabelValues(normHost).Observe(durationSec)
	}

	if evt.ReqBytes > 0 {
		c.reqBytesTotal.WithLabelValues(normHost).Add(float64(evt.ReqBytes))
	}
	if evt.ResBytes > 0 {
		c.resBytesTotal.WithLabelValues(normHost).Add(float64(evt.ResBytes))
	}
}

func (c *Collector) RecordMessage(status string) {
	c.messagesTotal.WithLabelValues(status).Inc()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./pkg/metrics`  
Expected: PASS

---

### Task 4: SPOP Protocol Handler

**Files:**
- Create: `pkg/spoa/handler.go`
- Test: `pkg/spoa/handler_test.go`

**Interfaces:**
- Consumes: `pkg/metrics.Collector`, `github.com/dropmorepackets/haproxy-go/spop`, `github.com/dropmorepackets/haproxy-go/pkg/encoding`
- Produces: `type Handler struct`, `NewHandler(collector *metrics.Collector) *Handler`, `(h *Handler) HandleSPOE(ctx context.Context, w *encoding.ActionWriter, m *encoding.Message)`

- [ ] **Step 1: Write unit test for SPOP handler routing and unmarshaling**

Create `pkg/spoa/handler_test.go`:
```go
package spoa

import (
	"context"
	"testing"

	"github.com/dropmorepackets/haproxy-go/pkg/encoding"
	"github.com/mrpk1906/haproxy-metrics-spoa/pkg/metrics"
	"github.com/mrpk1906/haproxy-metrics-spoa/pkg/normalizer"
	"github.com/prometheus/client_golang/prometheus"
)

func TestHandlerIgnoresUnrecognizedMessage(t *testing.T) {
	guard := normalizer.NewGuard(normalizer.Config{MaxTrackedHosts: 10, GroupIPs: true})
	reg := prometheus.NewRegistry()
	col := metrics.NewCollector(guard, reg)
	h := NewHandler(col)

	// An empty or different message name should simply be skipped without panic
	msg := encoding.AcquireMessage()
	defer encoding.ReleaseMessage(msg)

	h.HandleSPOE(context.Background(), nil, msg)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather err: %v", err)
	}

	for _, mf := range mfs {
		if mf.GetName() == "haproxy_host_http_requests_total" && len(mf.Metric) > 0 {
			t.Fatal("unexpected requests recorded for unrecognized message")
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/spoa`  
Expected: FAIL (undefined: NewHandler)

- [ ] **Step 3: Implement pkg/spoa/handler.go**

Create `pkg/spoa/handler.go`:
```go
package spoa

import (
	"context"

	"github.com/dropmorepackets/haproxy-go/pkg/encoding"
	"github.com/mrpk1906/haproxy-metrics-spoa/pkg/metrics"
)

const TargetMessageName = "http-response-metric"

type Handler struct {
	collector *metrics.Collector
}

func NewHandler(collector *metrics.Collector) *Handler {
	return &Handler{collector: collector}
}

func (h *Handler) HandleSPOE(ctx context.Context, w *encoding.ActionWriter, m *encoding.Message) {
	if !m.NameEquals(TargetMessageName) {
		h.collector.RecordMessage("ignored")
		return
	}

	var evt metrics.HTTPMetricEvent
	if err := m.KV.Unmarshal(&evt); err != nil {
		h.collector.RecordMessage("malformed")
		return
	}

	h.collector.RecordEvent(evt)
	h.collector.RecordMessage("ok")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./pkg/spoa`  
Expected: PASS

---

### Task 5: Server Infrastructure (UNIX/TCP Listener & Metrics HTTP Server)

**Files:**
- Create: `pkg/server/server.go`
- Test: `pkg/server/server_test.go`

**Interfaces:**
- Consumes: `pkg/spoa.Handler`, `github.com/dropmorepackets/haproxy-go/spop`, `net/http`
- Produces:
  - `type Config struct { SPOEListen string; SocketMode os.FileMode; MetricsListen string; MetricsPath string }`
  - `type Server struct`
  - `NewServer(cfg Config, handler *spoa.Handler, reg *prometheus.Registry) (*Server, error)`
  - `(s *Server) Start(ctx context.Context) error`
  - `(s *Server) Shutdown(ctx context.Context) error`

- [ ] **Step 1: Write failing test for server startup and scrape**

Create `pkg/server/server_test.go`:
```go
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

	"github.com/mrpk1906/haproxy-metrics-spoa/pkg/metrics"
	"github.com/mrpk1906/haproxy-metrics-spoa/pkg/normalizer"
	"github.com/mrpk1906/haproxy-metrics-spoa/pkg/spoa"
	"github.com/prometheus/client_golang/prometheus"
)

func TestServerStartAndScrapeMetrics(t *testing.T) {
	tempDir := t.TempDir()
	sockPath := filepath.Join(tempDir, "test-spoa.sock")

	guard := normalizer.NewGuard(normalizer.Config{MaxTrackedHosts: 10, GroupIPs: true})
	reg := prometheus.NewRegistry()
	col := metrics.NewCollector(guard, reg)
	h := spoa.NewHandler(col)

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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	// Wait for listener to be ready
	time.Sleep(100 * time.Millisecond)

	// Check if UNIX socket was created
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		t.Fatalf("expected unix socket file at %s", sockPath)
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/server`  
Expected: FAIL (undefined: Config, NewServer)

- [ ] **Step 3: Implement pkg/server/server.go**

Create `pkg/server/server.go`:
```go
package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/dropmorepackets/haproxy-go/spop"
	"github.com/mrpk1906/haproxy-metrics-spoa/pkg/spoa"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Config struct {
	SPOEListen    string
	SocketMode    os.FileMode
	MetricsListen string
	MetricsPath   string
}

type Server struct {
	cfg         Config
	handler     *spoa.Handler
	reg         *prometheus.Registry
	spopAgent   *spop.Agent
	spoeListener net.Listener
	httpServer  *http.Server
	isUnixSock  bool
	unixSockPath string
	mu          sync.Mutex
}

func NewServer(cfg Config, handler *spoa.Handler, reg *prometheus.Registry) (*Server, error) {
	return &Server{
		cfg:     cfg,
		handler: handler,
		reg:     reg,
	}, nil
}

func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()

	// 1. Setup SPOP Listener
	listenURL := s.cfg.SPOEListen
	if !strings.Contains(listenURL, "://") {
		listenURL = "unix://" + listenURL
	}
	u, err := url.Parse(listenURL)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("invalid spoe listen url: %w", err)
	}

	var listener net.Listener
	switch u.Scheme {
	case "unix":
		sockPath := u.Path
		if sockPath == "" {
			sockPath = u.Host
		}
		s.isUnixSock = true
		s.unixSockPath = sockPath
		_ = os.Remove(sockPath)
		listener, err = net.Listen("unix", sockPath)
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("listen unix socket error: %w", err)
		}
		mode := s.cfg.SocketMode
		if mode == 0 {
			mode = 0660
		}
		_ = os.Chmod(sockPath, mode)
	case "tcp":
		listener, err = net.Listen("tcp", u.Host)
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("listen tcp socket error: %w", err)
		}
	default:
		s.mu.Unlock()
		return fmt.Errorf("unsupported scheme %q (expected unix or tcp)", u.Scheme)
	}

	s.spoeListener = listener
	s.spopAgent = &spop.Agent{
		Handler:     spop.HandlerFunc(s.handler.HandleSPOE),
		BaseContext: ctx,
	}

	// 2. Setup HTTP Metrics Server
	mux := http.NewServeMux()
	metricsPath := s.cfg.MetricsPath
	if metricsPath == "" {
		metricsPath = "/metrics"
	}
	mux.Handle(metricsPath, promhttp.HandlerFor(s.reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK\n"))
	})

	httpListener, err := net.Listen("tcp", s.cfg.MetricsListen)
	if err != nil {
		_ = s.spoeListener.Close()
		s.mu.Unlock()
		return fmt.Errorf("listen http metrics error: %w", err)
	}

	s.httpServer = &http.Server{
		Handler: mux,
	}
	s.mu.Unlock()

	errCh := make(chan error, 2)

	go func() {
		if err := s.spopAgent.Serve(listener); err != nil && err != net.ErrClosed {
			errCh <- fmt.Errorf("spop agent exited with error: %w", err)
		}
	}()

	go func() {
		if err := s.httpServer.Serve(httpListener); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("http metrics server exited with error: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

func (s *Server) MetricsAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.httpServer == nil {
		return ""
	}
	// Retrieve bound address
	return s.cfg.MetricsListen
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var errs []string
	if s.spoeListener != nil {
		if err := s.spoeListener.Close(); err != nil && err != net.ErrClosed {
			errs = append(errs, fmt.Sprintf("close spoe listener: %v", err))
		}
	}
	if s.isUnixSock && s.unixSockPath != "" {
		_ = os.Remove(s.unixSockPath)
	}
	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Sprintf("shutdown http server: %v", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("shutdown errors: %s", strings.Join(errs, "; "))
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./pkg/server`  
Expected: PASS

---

### Task 6: CLI Binary & Entrypoint

**Files:**
- Create: `cmd/spoa/main.go`
- Test: `cmd/spoa/main_test.go`

**Interfaces:**
- Consumes: `pkg/normalizer`, `pkg/metrics`, `pkg/spoa`, `pkg/server`, `internal/version`
- Produces: Executable binary `bin/haproxy-metrics-spoa`

- [ ] **Step 1: Write integration smoke test for CLI flags**

Create `cmd/spoa/main_test.go`:
```go
package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCLIVersionFlag(t *testing.T) {
	cmd := exec.Command("go", "run", "main.go", "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run failed: %v, output: %s", err, string(out))
	}
	if !strings.Contains(string(out), "haproxy-metrics-spoa") {
		t.Fatalf("expected version output, got: %s", string(out))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/spoa`  
Expected: FAIL (cannot find package main or main.go)

- [ ] **Step 3: Implement cmd/spoa/main.go**

Create `cmd/spoa/main.go`:
```go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mrpk1906/haproxy-metrics-spoa/internal/version"
	"github.com/mrpk1906/haproxy-metrics-spoa/pkg/metrics"
	"github.com/mrpk1906/haproxy-metrics-spoa/pkg/normalizer"
	"github.com/mrpk1906/haproxy-metrics-spoa/pkg/server"
	"github.com/mrpk1906/haproxy-metrics-spoa/pkg/spoa"
	"github.com/prometheus/client_golang/prometheus"
)

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func main() {
	var (
		spoeListen    = flag.String("spoe.listen", getEnv("SPOA_LISTEN", "unix:///var/run/haproxy/spoa.sock"), "SPOE listen URL (unix:///path or tcp://host:port)")
		socketMode    = flag.Uint("spoe.socket-mode", 0660, "UNIX socket file permission mode")
		metricsListen = flag.String("metrics.listen", getEnv("METRICS_LISTEN", ":9101"), "Address to serve Prometheus metrics")
		metricsPath   = flag.String("metrics.path", getEnv("METRICS_PATH", "/metrics"), "HTTP path for metrics")
		maxHosts      = flag.Int("cardinality.max-hosts", 5000, "Maximum distinct hosts to track before grouping into _overflow_")
		groupIPs      = flag.Bool("cardinality.group-ips", true, "Group raw IPv4/IPv6 hosts into _ip_")
		showVersion   = flag.Bool("version", false, "Print version information and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Info())
		os.Exit(0)
	}

	log.Printf("Starting %s", version.Info())
	log.Printf("Config: SPOE listen=%s, metrics=%s%s, max-hosts=%d, group-ips=%v",
		*spoeListen, *metricsListen, *metricsPath, *maxHosts, *groupIPs)

	guard := normalizer.NewGuard(normalizer.Config{
		MaxTrackedHosts: *maxHosts,
		GroupIPs:        *groupIPs,
	})

	reg := prometheus.DefaultRegisterer
	collector := metrics.NewCollector(guard, reg)
	handler := spoa.NewHandler(collector)

	cfg := server.Config{
		SPOEListen:    *spoeListen,
		SocketMode:    os.FileMode(*socketMode),
		MetricsListen: *metricsListen,
		MetricsPath:   *metricsPath,
	}

	srv, err := server.NewServer(cfg, handler, prometheus.DefaultGatherer.(*prometheus.Registry))
	if err != nil {
		log.Fatalf("Failed to initialize server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Printf("Received signal %s, initiating graceful shutdown...", sig)
		cancel()
	}()

	if err := srv.Start(ctx); err != nil {
		log.Printf("Server stopped with error: %v", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	log.Println("haproxy-metrics-spoa terminated gracefully.")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./cmd/spoa`  
Expected: PASS

---

### Task 7: HAProxy Configuration Examples & Verification Suite

**Files:**
- Create: `examples/haproxy/haproxy.cfg`
- Create: `examples/haproxy/spoe-metrics.cfg`
- Create: `README.md`
- Test: Automated end-to-end verification script or `go test ./...`

**Interfaces:**
- Produces: Production-ready HAProxy config files and README documentation.

- [ ] **Step 1: Create examples/haproxy/spoe-metrics.cfg**

```haproxy
[metrics]
spoe-agent metrics-agent
    messages http-response-metric
    option   async
    timeout  hello      100ms
    timeout  idle       30s
    timeout  processing 50ms
    use-backend spoe-metrics-backend

spoe-message http-response-metric
    args host=var(txn.host) method=method status=status lat=lat res_bytes=res.payload_lv req_bytes=req.payload_lv
    event on-http-response

backend spoe-metrics-backend
    mode tcp
    server spoa1 /var/run/haproxy/spoa.sock check
```

- [ ] **Step 2: Create examples/haproxy/haproxy.cfg**

```haproxy
global
    log stdout format raw local0
    maxconn 4096

defaults
    log     global
    mode    http
    timeout connect 5000ms
    timeout client  50000ms
    timeout server  50000ms

frontend fe_http
    bind :80
    mode http

    # Capture Host header into transaction variable
    http-request set-var(txn.host) req.hdr(Host)

    # Attach SPOE metrics filter
    filter spoe engine metrics config /etc/haproxy/spoe-metrics.cfg

    default_backend be_app

backend be_app
    mode http
    server s1 127.0.0.1:8080 check
```

- [ ] **Step 3: Create README.md**

Write comprehensive documentation explaining build steps, configuration, and metric descriptions.

- [ ] **Step 4: Run all project tests**

Run: `go test -v ./...`  
Expected: ALL PASS
