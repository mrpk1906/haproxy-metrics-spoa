# Docker End-to-End Testing Suite & Makefile Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a complete, automated Docker-based End-to-End (E2E) testing suite and a root Makefile for `haproxy-metrics-spoa` that validates SPOA metric collection over both TCP and UNIX domain sockets across various HTTP traffic conditions.

**Architecture:** A multi-stage `Dockerfile` compiles the production daemon and a lightweight mock backend HTTP service. A unified `docker-compose.e2e.yml` sets up HAProxy with two frontends routing SPOP traffic to two SPOA instances (one TCP, one UNIX domain socket via shared volume). A Go test suite (`test/e2e/e2e_test.go`, tagged with `//go:build e2e`) orchestrates the containers, drives HTTP traffic, and parses Prometheus metrics to verify host normalization, cardinality overflow, and RED metrics.

**Tech Stack:** Go 1.24+, Docker, Docker Compose, HAProxy 2.8-alpine, `github.com/prometheus/client_golang`, `github.com/prometheus/common/expfmt`.

**Spec:** [`docs/superpowers/specs/2026-10-03-docker-e2e-testing-design.md`](file:///Users/mrpk1906/Workspace/haproxy-metrics-spoa/docs/superpowers/specs/2026-10-03-docker-e2e-testing-design.md)

## Global Constraints

- Zero CGo dependencies (`CGO_ENABLED=0`).
- No extra external container dependencies in `go.mod` (use `os/exec` with Docker Compose CLI).
- Protect E2E test files with `//go:build e2e` so standard `go test ./...` remains fast and pure Go.
- High-range host port bindings (`18080`, `18081`, `19100`, `19101`) to prevent local port collisions.
- Socket volume directory permissions must allow HAProxy non-root read/write (`0777`).

---

### Task 1: Mock Backend Server

**Files:**
- Create: `test/e2e/mockbackend/main.go`
- Test: `test/e2e/mockbackend/main_test.go`

**Interfaces:**
- Consumes: Standard library `net/http`, `strconv`, `time`, `io`.
- Produces: Executable HTTP server binary `/bin/mock-backend` listening on `:8080` with routes:
  - `GET /` -> 200 OK `"mock-backend-ok\n"`
  - `POST /echo` -> 200 OK with echoed request body
  - `GET /status/{code}` -> status `{code}` with body `status code {code}\n`
  - `GET /delay/{ms}` -> 200 OK after sleeping `{ms}` milliseconds
  - `GET /healthz` -> 200 OK `"healthy\n"`

- [ ] **Step 1: Write unit tests for mock backend handlers**

Create `test/e2e/mockbackend/main_test.go`:
```go
package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMockBackendHandlers(t *testing.T) {
	mux := setupRouter()

	t.Run("GET /", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if rec.Body.String() != "mock-backend-ok\n" {
			t.Fatalf("expected body 'mock-backend-ok\\n', got %q", rec.Body.String())
		}
	})

	t.Run("POST /echo", func(t *testing.T) {
		payload := []byte("hello payload")
		req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if !bytes.Equal(rec.Body.Bytes(), payload) {
			t.Fatalf("expected echoed body %q, got %q", string(payload), rec.Body.String())
		}
	})

	t.Run("GET /status/404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/status/404", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", rec.Code)
		}
	})

	t.Run("GET /status/500", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/status/500", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected status 500, got %d", rec.Code)
		}
	})

	t.Run("GET /delay/50", func(t *testing.T) {
		start := time.Now()
		req := httptest.NewRequest(http.MethodGet, "/delay/50", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		elapsed := time.Since(start)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if elapsed < 40*time.Millisecond {
			t.Fatalf("expected delay >= 40ms, got %v", elapsed)
		}
	})

	t.Run("GET /healthz", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./test/e2e/mockbackend`
Expected: FAIL with "setupRouter undefined" or build failure.

- [ ] **Step 3: Implement mock backend in `test/e2e/mockbackend/main.go`**

```go
package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func setupRouter() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("healthy\n"))
	})

	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, r.Body)
	})

	mux.HandleFunc("/status/", func(w http.ResponseWriter, r *http.Request) {
		codeStr := strings.TrimPrefix(r.URL.Path, "/status/")
		code, err := strconv.Atoi(codeStr)
		if err != nil || code < 100 || code > 599 {
			code = http.StatusBadRequest
		}
		w.WriteHeader(code)
		_, _ = fmt.Fprintf(w, "status code %d\n", code)
	})

	mux.HandleFunc("/delay/", func(w http.ResponseWriter, r *http.Request) {
		msStr := strings.TrimPrefix(r.URL.Path, "/delay/")
		ms, err := strconv.Atoi(msStr)
		if err == nil && ms > 0 {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("delay-ok\n"))
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("mock-backend-ok\n"))
	})

	return mux
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           setupRouter(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("Starting mock backend on port %s", port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -v ./test/e2e/mockbackend`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add test/e2e/mockbackend/
git commit -m "feat(e2e): add mock backend HTTP server for end-to-end testing"
```

---

### Task 2: Multi-Stage Dockerfile & HAProxy Configuration

**Files:**
- Create: `Dockerfile`
- Create: `test/e2e/haproxy/haproxy.cfg`
- Create: `test/e2e/haproxy/spoe-metrics-tcp.cfg`
- Create: `test/e2e/haproxy/spoe-metrics-unix.cfg`
- Create: `test/e2e/docker-compose.e2e.yml`

**Interfaces:**
- Consumes: `./cmd/spoa`, `./test/e2e/mockbackend`.
- Produces:
  - Docker images: `haproxy-metrics-spoa` (`daemon` target) and `mock-backend` (`mock-backend` target).
  - Docker Compose topology with services: `mock-backend`, `spoa-tcp`, `spoa-unix`, `haproxy`.

- [ ] **Step 1: Create `Dockerfile`**

```dockerfile
# Stage 1: Builder
FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=1.0.0
ARG GIT_COMMIT=dev
ARG BUILD_DATE=now
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-X github.com/mrpk1906/haproxy-metrics-spoa/internal/version.Version=${VERSION} \
              -X github.com/mrpk1906/haproxy-metrics-spoa/internal/version.GitCommit=${GIT_COMMIT} \
              -X github.com/mrpk1906/haproxy-metrics-spoa/internal/version.BuildDate=${BUILD_DATE}" \
    -o /bin/haproxy-metrics-spoa ./cmd/spoa
RUN CGO_ENABLED=0 GOOS=linux go build \
    -o /bin/mock-backend ./test/e2e/mockbackend

# Stage 2: Production daemon image
FROM alpine:3.21 AS daemon
RUN apk --no-cache add ca-certificates tzdata
RUN mkdir -p /var/run/haproxy && chmod 777 /var/run/haproxy
COPY --from=builder /bin/haproxy-metrics-spoa /usr/local/bin/haproxy-metrics-spoa
ENTRYPOINT ["/usr/local/bin/haproxy-metrics-spoa"]

# Stage 3: Mock backend image
FROM alpine:3.21 AS mock-backend
COPY --from=builder /bin/mock-backend /usr/local/bin/mock-backend
ENTRYPOINT ["/usr/local/bin/mock-backend"]
```

- [ ] **Step 2: Create HAProxy SPOP config files**

Create `test/e2e/haproxy/spoe-metrics-tcp.cfg`:
```haproxy
[metrics-tcp]
spoe-agent metrics-agent-tcp
    messages http-response-metric
    option   async
    timeout  hello      100ms
    timeout  idle       30s
    timeout  processing 50ms
    use-backend spoe-metrics-tcp-backend

spoe-message http-response-metric
    args host=var(txn.host) method=method status=status lat=lat res_bytes=res.payload_lv req_bytes=req.payload_lv
    event on-http-response
```

Create `test/e2e/haproxy/spoe-metrics-unix.cfg`:
```haproxy
[metrics-unix]
spoe-agent metrics-agent-unix
    messages http-response-metric
    option   async
    timeout  hello      100ms
    timeout  idle       30s
    timeout  processing 50ms
    use-backend spoe-metrics-unix-backend

spoe-message http-response-metric
    args host=var(txn.host) method=method status=status lat=lat res_bytes=res.payload_lv req_bytes=req.payload_lv
    event on-http-response
```

- [ ] **Step 3: Create `test/e2e/haproxy/haproxy.cfg`**

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

frontend fe_tcp
    bind :8080
    mode http
    http-request set-var(txn.host) req.hdr(Host)
    filter spoe engine metrics-tcp config /usr/local/etc/haproxy/spoe-metrics-tcp.cfg
    default_backend be_mock_backend

frontend fe_unix
    bind :8081
    mode http
    http-request set-var(txn.host) req.hdr(Host)
    filter spoe engine metrics-unix config /usr/local/etc/haproxy/spoe-metrics-unix.cfg
    default_backend be_mock_backend

backend be_mock_backend
    mode http
    server s1 mock-backend:8080 check

backend spoe-metrics-tcp-backend
    mode tcp
    server spoa_tcp spoa-tcp:9100 check

backend spoe-metrics-unix-backend
    mode tcp
    server spoa_unix /var/run/haproxy/spoa.sock check
```

- [ ] **Step 4: Create `test/e2e/docker-compose.e2e.yml`**

```yaml
services:
  mock-backend:
    build:
      context: ../..
      dockerfile: Dockerfile
      target: mock-backend
    networks:
      - e2e_net

  spoa-tcp:
    build:
      context: ../..
      dockerfile: Dockerfile
      target: daemon
    command:
      - "--spoe.listen=tcp://0.0.0.0:9100"
      - "--metrics.listen=0.0.0.0:9101"
      - "--max-hosts=10"
    ports:
      - "19100:9101"
    networks:
      - e2e_net

  spoa-unix:
    build:
      context: ../..
      dockerfile: Dockerfile
      target: daemon
    command:
      - "--spoe.listen=unix:///var/run/haproxy/spoa.sock"
      - "--metrics.listen=0.0.0.0:9101"
      - "--max-hosts=10"
    volumes:
      - spoa_socket:/var/run/haproxy
    ports:
      - "19101:9101"
    networks:
      - e2e_net

  haproxy:
    image: haproxy:2.8-alpine
    user: root
    volumes:
      - ./haproxy/haproxy.cfg:/usr/local/etc/haproxy/haproxy.cfg:ro
      - ./haproxy/spoe-metrics-tcp.cfg:/usr/local/etc/haproxy/spoe-metrics-tcp.cfg:ro
      - ./haproxy/spoe-metrics-unix.cfg:/usr/local/etc/haproxy/spoe-metrics-unix.cfg:ro
      - spoa_socket:/var/run/haproxy
    ports:
      - "18080:8080"
      - "18081:8081"
    networks:
      - e2e_net
    depends_on:
      - mock-backend
      - spoa-tcp
      - spoa-unix

volumes:
  spoa_socket:

networks:
  e2e_net:
```

- [ ] **Step 5: Verify compose configuration syntax**

Run: `docker compose -f test/e2e/docker-compose.e2e.yml config`
Expected: Valid YAML output, exit code 0.

- [ ] **Step 6: Commit**

```bash
git add Dockerfile test/e2e/haproxy/ test/e2e/docker-compose.e2e.yml
git commit -m "feat(e2e): add dockerfile, haproxy configs, and docker compose topology"
```

---

### Task 3: Go E2E Test Suite

**Files:**
- Create: `test/e2e/e2e_test.go`

**Interfaces:**
- Consumes: `docker compose -f test/e2e/docker-compose.e2e.yml`, `github.com/prometheus/common/expfmt`.
- Produces: Test runner executable via `go test -tags=e2e -v ./test/e2e/...`.

- [ ] **Step 1: Write `test/e2e/e2e_test.go`**

```go
//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

const (
	composeFile   = "docker-compose.e2e.yml"
	haproxyTCP    = "http://127.0.0.1:18080"
	haproxyUnix   = "http://127.0.0.1:18081"
	spoaTCPMetric = "http://127.0.0.1:19100"
	spoaUnixMetric= "http://127.0.0.1:19101"
)

func TestMain(m *testing.M) {
	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Println("docker not found in PATH, skipping E2E tests")
		os.Exit(0)
	}

	fmt.Println("Starting Docker Compose E2E environment...")
	downCmd := exec.Command("docker", "compose", "-f", composeFile, "down", "-v")
	_ = downCmd.Run()

	upCmd := exec.Command("docker", "compose", "-f", composeFile, "up", "-d", "--build")
	upCmd.Stdout = os.Stdout
	upCmd.Stderr = os.Stderr
	if err := upCmd.Run(); err != nil {
		fmt.Printf("Failed to start docker compose: %v\n", err)
		_ = exec.Command("docker", "compose", "-f", composeFile, "down", "-v").Run()
		os.Exit(1)
	}

	defer func() {
		fmt.Println("Tearing down Docker Compose E2E environment...")
		_ = exec.Command("docker", "compose", "-f", composeFile, "down", "-v").Run()
	}()

	if err := waitForReadiness(); err != nil {
		fmt.Printf("Environment failed readiness checks: %v\n", err)
		logsCmd := exec.Command("docker", "compose", "-f", composeFile, "logs")
		logsCmd.Stdout = os.Stdout
		_ = logsCmd.Run()
		os.Exit(1)
	}

	code := m.Run()
	os.Exit(code)
}

func waitForReadiness() error {
	client := &http.Client{Timeout: 1 * time.Second}
	endpoints := []string{
		spoaTCPMetric + "/healthz",
		spoaUnixMetric + "/healthz",
		haproxyTCP + "/healthz",
		haproxyUnix + "/healthz",
	}

	deadline := time.Now().Add(45 * time.Second)
	for _, ep := range endpoints {
		ready := false
		for time.Now().Before(deadline) {
			resp, err := client.Get(ep)
			if err == nil && resp.StatusCode == http.StatusOK {
				_ = resp.Body.Close()
				ready = true
				break
			}
			if resp != nil {
				_ = resp.Body.Close()
			}
			time.Sleep(500 * time.Millisecond)
		}
		if !ready {
			return fmt.Errorf("timeout waiting for %s", ep)
		}
	}
	// Small buffer for HAProxy SPOE filter session stabilization
	time.Sleep(1 * time.Second)
	return nil
}

func fetchMetrics(endpoint string) (map[string]*dto.MetricFamily, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(endpoint + "/metrics")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metrics: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected metrics status: %d", resp.StatusCode)
	}

	var parser expfmt.TextParser
	return parser.TextToMetricFamilies(resp.Body)
}

func findMetricValue(families map[string]*dto.MetricFamily, name string, labels map[string]string) (float64, bool) {
	family, ok := families[name]
	if !ok {
		return 0, false
	}

	for _, m := range family.GetMetric() {
		matched := true
		for k, v := range labels {
			labelMatched := false
			for _, lbl := range m.GetLabel() {
				if lbl.GetName() == k && lbl.GetValue() == v {
					labelMatched = true
					break
				}
			}
			if !labelMatched {
				matched = false
				break
			}
		}
		if matched {
			if m.GetCounter() != nil {
				return m.GetCounter().GetValue(), true
			}
			if m.GetGauge() != nil {
				return m.GetGauge().GetValue(), true
			}
			if m.GetHistogram() != nil {
				return float64(m.GetHistogram().GetSampleCount()), true
			}
		}
	}
	return 0, false
}

func sendRequest(t *testing.T, targetURL, host, method string, body []byte) (int, string) {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, targetURL, bodyReader)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	if host != "" {
		req.Host = host
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(respBody)
}

func TestE2E_TCP_BasicFlow(t *testing.T) {
	host := "service-tcp.example.com"
	for i := 0; i < 5; i++ {
		status, body := sendRequest(t, haproxyTCP+"/", host, http.MethodGet, nil)
		if status != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", status, body)
		}
	}

	// Give SPOA brief time to consume async SPOP events
	time.Sleep(200 * time.Millisecond)

	metrics, err := fetchMetrics(spoaTCPMetric)
	if err != nil {
		t.Fatalf("failed to fetch SPOA TCP metrics: %v", err)
	}

	reqs, ok := findMetricValue(metrics, "haproxy_host_http_requests_total", map[string]string{
		"host":   host,
		"code":   "200",
		"method": "GET",
	})
	if !ok || reqs != 5 {
		t.Fatalf("expected 5 requests for host %s, got %v (found: %v)", host, reqs, ok)
	}

	reqBytes, ok := findMetricValue(metrics, "haproxy_host_http_request_bytes_total", map[string]string{"host": host})
	if !ok || reqBytes <= 0 {
		t.Fatalf("expected request bytes > 0, got %v", reqBytes)
	}

	resBytes, ok := findMetricValue(metrics, "haproxy_host_http_response_bytes_total", map[string]string{"host": host})
	if !ok || resBytes <= 0 {
		t.Fatalf("expected response bytes > 0, got %v", resBytes)
	}

	histCount, ok := findMetricValue(metrics, "haproxy_host_http_request_duration_seconds", map[string]string{"host": host})
	if !ok || histCount != 5 {
		t.Fatalf("expected histogram sample count 5, got %v", histCount)
	}
}

func TestE2E_UnixSocket_BasicFlow(t *testing.T) {
	host := "service-unix.internal.net"
	for i := 0; i < 5; i++ {
		status, body := sendRequest(t, haproxyUnix+"/", host, http.MethodGet, nil)
		if status != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", status, body)
		}
	}

	time.Sleep(200 * time.Millisecond)

	metrics, err := fetchMetrics(spoaUnixMetric)
	if err != nil {
		t.Fatalf("failed to fetch SPOA UNIX metrics: %v", err)
	}

	reqs, ok := findMetricValue(metrics, "haproxy_host_http_requests_total", map[string]string{
		"host":   host,
		"code":   "200",
		"method": "GET",
	})
	if !ok || reqs != 5 {
		t.Fatalf("expected 5 requests for host %s over UNIX socket, got %v", host, reqs)
	}
}

func TestE2E_HostNormalization(t *testing.T) {
	testCases := []struct {
		rawHost        string
		expectedMetric string
	}{
		{"billing.norm-test.org:8443", "billing.norm-test.org"},
		{"192.168.1.100:80", "_ipv4_"},
		{"[2001:db8::1]:443", "_ipv6_"},
		{"UPPERCASE.NORM-TEST.ORG", "uppercase.norm-test.org"},
		{"trailing.norm-test.org.", "trailing.norm-test.org"},
	}

	for _, tc := range testCases {
		status, _ := sendRequest(t, haproxyTCP+"/", tc.rawHost, http.MethodGet, nil)
		if status != http.StatusOK {
			t.Fatalf("request failed for host %s with status %d", tc.rawHost, status)
		}
	}

	time.Sleep(200 * time.Millisecond)

	metrics, err := fetchMetrics(spoaTCPMetric)
	if err != nil {
		t.Fatalf("failed to fetch metrics: %v", err)
	}

	for _, tc := range testCases {
		val, ok := findMetricValue(metrics, "haproxy_host_http_requests_total", map[string]string{
			"host": tc.expectedMetric,
			"code": "200",
		})
		if !ok || val < 1 {
			t.Errorf("expected normalized host %q to have recorded metric, got %v (found: %v)", tc.expectedMetric, val, ok)
		}
	}
}

func TestE2E_CardinalityOverflow(t *testing.T) {
	// The daemon runs with --max-hosts=10. Send requests to 15 unique hosts.
	for i := 1; i <= 15; i++ {
		h := fmt.Sprintf("tenant-%02d.overflow-test.com", i)
		sendRequest(t, haproxyTCP+"/", h, http.MethodGet, nil)
	}

	time.Sleep(300 * time.Millisecond)

	metrics, err := fetchMetrics(spoaTCPMetric)
	if err != nil {
		t.Fatalf("failed to fetch metrics: %v", err)
	}

	overflowVal, ok := findMetricValue(metrics, "haproxy_host_http_requests_total", map[string]string{
		"host": "_overflow_",
		"code": "200",
	})
	if !ok || overflowVal < 1 {
		t.Fatalf("expected overflow requests to be recorded under _overflow_, got %v (found: %v)", overflowVal, ok)
	}

	trackedTotal, ok := findMetricValue(metrics, "haproxy_host_tracked_total", nil)
	if !ok || trackedTotal != 10 {
		t.Fatalf("expected haproxy_host_tracked_total to equal 10, got %v", trackedTotal)
	}

	overflowGauge, ok := findMetricValue(metrics, "haproxy_host_overflow_total", nil)
	if !ok || overflowGauge < 1 {
		t.Fatalf("expected haproxy_host_overflow_total gauge >= 1, got %v", overflowGauge)
	}
}

func TestE2E_StatusCodesAndMethods(t *testing.T) {
	host := "status-test.domain.com"

	sendRequest(t, haproxyTCP+"/status/404", host, http.MethodGet, nil)
	sendRequest(t, haproxyTCP+"/status/500", host, http.MethodPost, []byte("request-body"))

	time.Sleep(200 * time.Millisecond)

	metrics, err := fetchMetrics(spoaTCPMetric)
	if err != nil {
		t.Fatalf("failed to fetch metrics: %v", err)
	}

	val404, ok := findMetricValue(metrics, "haproxy_host_http_requests_total", map[string]string{
		"host":   host,
		"code":   "404",
		"method": "GET",
	})
	if !ok || val404 != 1 {
		t.Fatalf("expected 1 GET 404 request, got %v (found: %v)", val404, ok)
	}

	val500, ok := findMetricValue(metrics, "haproxy_host_http_requests_total", map[string]string{
		"host":   host,
		"code":   "500",
		"method": "POST",
	})
	if !ok || val500 != 1 {
		t.Fatalf("expected 1 POST 500 request, got %v (found: %v)", val500, ok)
	}
}

func TestE2E_LatencyHistogram(t *testing.T) {
	host := "delay-test.domain.com"
	sendRequest(t, haproxyTCP+"/delay/50", host, http.MethodGet, nil)

	time.Sleep(200 * time.Millisecond)

	metrics, err := fetchMetrics(spoaTCPMetric)
	if err != nil {
		t.Fatalf("failed to fetch metrics: %v", err)
	}

	family, ok := metrics["haproxy_host_http_request_duration_seconds"]
	if !ok {
		t.Fatalf("haproxy_host_http_request_duration_seconds metric family not found")
	}

	foundBucket := false
	for _, m := range family.GetMetric() {
		for _, lbl := range m.GetLabel() {
			if lbl.GetName() == "host" && lbl.GetValue() == host {
				hist := m.GetHistogram()
				if hist != nil && hist.GetSampleCount() >= 1 {
					foundBucket = true
					// Sample sum should be at least ~0.045s
					if hist.GetSampleSum() < 0.040 {
						t.Errorf("expected sample sum >= 0.040s, got %f", hist.GetSampleSum())
					}
				}
			}
		}
	}

	if !foundBucket {
		t.Fatalf("failed to find histogram metric for host %s", host)
	}
}
```

- [ ] **Step 2: Commit**

```bash
git add test/e2e/e2e_test.go
git commit -m "feat(e2e): add Go end-to-end integration test suite with build tag e2e"
```

---

### Task 4: Makefile & Developer Workflows

**Files:**
- Create: `Makefile`

**Interfaces:**
- Consumes: Go toolchain, Docker Compose CLI.
- Produces: Makefile targets:
  - `build`, `test`, `test-race`, `test-e2e`, `docker-build`, `docker-e2e-up`, `docker-e2e-down`, `docker-e2e-logs`, `fmt`, `vet`, `clean`.

- [ ] **Step 1: Create `Makefile`**

```makefile
BINARY_NAME := haproxy-metrics-spoa
BIN_DIR := bin
DOCKER_IMAGE := haproxy-metrics-spoa:latest
COMPOSE_E2E := docker compose -f test/e2e/docker-compose.e2e.yml

VERSION ?= 1.0.0
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X github.com/mrpk1906/haproxy-metrics-spoa/internal/version.Version=$(VERSION) \
           -X github.com/mrpk1906/haproxy-metrics-spoa/internal/version.GitCommit=$(GIT_COMMIT) \
           -X github.com/mrpk1906/haproxy-metrics-spoa/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: all build test test-race test-e2e docker-build docker-e2e-up docker-e2e-down docker-e2e-logs fmt vet clean

all: build test

build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/spoa

test:
	go test -v ./...

test-race:
	go test -race -v ./...

test-e2e:
	go test -tags=e2e -v -timeout=120s ./test/e2e/...

docker-build:
	docker build -t $(DOCKER_IMAGE) .

docker-e2e-up:
	$(COMPOSE_E2E) up -d --build

docker-e2e-down:
	$(COMPOSE_E2E) down -v

docker-e2e-logs:
	$(COMPOSE_E2E) logs -f

fmt:
	go fmt ./...

vet:
	go vet ./...

clean:
	rm -rf $(BIN_DIR)
	$(COMPOSE_E2E) down -v --remove-orphans 2>/dev/null || true
```

- [ ] **Step 2: Test `make build` and `make fmt`**

Run: `make build`
Expected: Successfully compiles `bin/haproxy-metrics-spoa`.

- [ ] **Step 3: Run full E2E test via `make test-e2e`**

Run: `make test-e2e`
Expected:
1. Docker Compose builds images and starts containers.
2. Readiness checks pass on all 4 endpoints.
3. All E2E subtests PASS.
4. Clean teardown with exit code 0.

- [ ] **Step 4: Commit**

```bash
git add Makefile
git commit -m "feat: add root Makefile for build, test, and containerized e2e workflows"
```
