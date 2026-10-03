# Design Specification: Docker End-to-End Testing Suite & Makefile

**Date:** 2026-10-03  
**Status:** Approved for Implementation  
**Project:** `haproxy-metrics-spoa`  
**Target Repository:** `/Users/mrpk1906/Workspace/haproxy-metrics-spoa`  

---

## 1. Executive Summary & Motivation

`haproxy-metrics-spoa` operates at the boundary between HAProxy's binary Stream Processing Offload Protocol (SPOP) and Prometheus scraping infrastructure. While unit tests validate internal components (`pkg/normalizer`, `pkg/metrics`, `pkg/spoa`, `pkg/server`), an automated End-to-End (E2E) test suite is necessary to verify the entire system in real production-like network environments.

This design introduces:
1. A multi-stage `Dockerfile` producing a production-ready SPOA daemon container and a lightweight mock backend container.
2. A multi-container `docker-compose.e2e.yml` topology orchestrating HAProxy, two SPOA instances (one TCP, one UNIX domain socket), and an upstream mock HTTP service.
3. A Go integration test suite (`test/e2e/e2e_test.go`) guarded by the `//go:build e2e` build tag that automatically spins up the environment, generates realistic HTTP traffic, and parses Prometheus metrics to verify host accounting, normalization, and cardinality limits.
4. A root `Makefile` providing standard build, test, race detection, E2E test, and container lifecycle targets.

---

## 2. Goals & Non-Goals

### Goals
- **Full-Stack Verification:** Validate that real HAProxy instances (official Docker image) send binary SPOP NOTIFY frames to `haproxy-metrics-spoa` upon receiving client HTTP traffic, and that SPOA parses them and exposes accurate Prometheus RED metrics.
- **Dual Transport Coverage:** Verify both TCP networking (`tcp://...`) and shared UNIX domain socket files (`unix:///...`) mounted via Docker volumes.
- **Feature Verification via E2E Traffic:**
  - Standard 200 OK traffic with request counts, durations, and request/response byte metrics.
  - Host normalization across edge cases (port stripping, IPv4 grouping, IPv6 grouping, case insensitivity, trailing dots).
  - Cardinality protection overflow behavior (`_overflow_` grouping when unique hosts exceed `--max-hosts`).
  - HTTP error codes (404, 500) and various HTTP methods (GET, POST).
  - Upstream latency histogram distribution.
- **Ergonomic Developer Workflows:** Provide single-command execution via `make test-e2e` or `go test -tags=e2e ./test/e2e/...`, with clean automatic teardown of Docker resources.
- **Zero Impact on Fast Unit Tests:** Guard all E2E tests behind Go build tags so `go test ./...` remains sub-second and requires neither Docker nor network access.

### Non-Goals
- **Load / Stress Benchmarking:** The E2E test is functional, not a performance benchmarking harness (e.g. not running 100k req/s).
- **External Dependency Bloat:** Avoid introducing heavy third-party container frameworks like `testcontainers-go` into `go.mod`; use standard library `os/exec` with Docker Compose CLI.

---

## 3. High-Level Architecture & Container Topology

```
+-----------------------------------------------------------------------------------------+
|                                Host / Test Process                                      |
|                                                                                         |
|   go test -tags=e2e ./test/e2e/...                                                      |
|       │                                                                                 |
|       ├─► HTTP Client Requests ──► HAProxy (:18080 TCP frontend / :18081 UNIX frontend) |
|       └─► Prometheus Scrapes ────► SPOA TCP (:19100/metrics) & SPOA UNIX (:19101/metrics)|
+────────────────────────────────────────┬────────────────────────────────────────────────+
                                         │ Docker Network & Shared Volume
                                         ▼
+─────────────────────────────────────────────────────────────────────────────────────────+
|                               Docker Compose Topology                                   |
|                                                                                         |
|   ┌──────────────────────────┐                                                          |
|   │       mock-backend       │◄─────────────────────────┐                               |
|   │         (:8080)          │                          │ HTTP proxy pass               |
|   └──────────────────────────┘                          │                               |
|                                                ┌────────┴────────┐                      |
|                                                │  haproxy-e2e    │                      |
|                                                │  :18080 (TCP)   │                      |
|                                                │  :18081 (UNIX)  │                      |
|                                                └────┬───────┬────┘                      |
|                                   SPOP over TCP     │       │ SPOP over UNIX socket     |
|                                   (spoa-tcp:9100)   │       │ (/var/run/haproxy/spoa.sock)|
|                                                     ▼       ▼                           |
|   ┌──────────────────────────┐                ┌──────────────────────────┐              |
|   │         spoa-tcp         │                │        spoa-unix         │              |
|   │  SPOE: :9100             │                │  SPOE: /var/run/...sock  │              |
|   │  Metrics: :19100         │                │  Metrics: :19101         │              |
|   │  max-hosts: 10           │                │  max-hosts: 10           │              |
|   └──────────────────────────┘                └──────────────────────────┘              |
|                                                (mounted shared volume)                  |
+-----------------------------------------------------------------------------------------+
```

---

## 4. Component Details

### 4.1 Root Multi-Stage `Dockerfile`

The root `Dockerfile` will define two build targets:

```dockerfile
# Stage 1: Build binaries
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

# Target: daemon (production runtime)
FROM alpine:3.21 AS daemon
RUN apk --no-cache add ca-certificates tzdata
RUN mkdir -p /var/run/haproxy && chmod 777 /var/run/haproxy
COPY --from=builder /bin/haproxy-metrics-spoa /usr/local/bin/haproxy-metrics-spoa
ENTRYPOINT ["/usr/local/bin/haproxy-metrics-spoa"]

# Target: mock-backend (test helper)
FROM alpine:3.21 AS mock-backend
COPY --from=builder /bin/mock-backend /usr/local/bin/mock-backend
ENTRYPOINT ["/usr/local/bin/mock-backend"]
```

### 4.2 Mock Backend Service (`test/e2e/mockbackend/main.go`)

A pure Go HTTP server providing controllable endpoints:
- `GET /`: returns status `200 OK` and static message `"mock-backend-ok\n"`.
- `POST /echo`: reads the request body and echoes it back in the response body (for verifying ingress & egress bytes).
- `GET /status/{code}`: responds with the requested status code (e.g. 404, 500).
- `GET /delay/{ms}`: sleeps for `{ms}` milliseconds before returning `200 OK` (for verifying latency histogram).
- `GET /healthz`: returns status `200 OK` `"healthy\n"`.

### 4.3 HAProxy & SPOP Configurations

Directory: `test/e2e/haproxy/`

1. **`spoe-metrics-tcp.cfg`**:
   Configures SPOP engine `metrics-tcp` targeting backend `spoe-metrics-tcp-backend`.
2. **`spoe-metrics-unix.cfg`**:
   Configures SPOP engine `metrics-unix` targeting backend `spoe-metrics-unix-backend`.
3. **`haproxy.cfg`**:
   - `frontend fe_tcp`:
     - Binds `0.0.0.0:8080`.
     - `http-request set-var(txn.host) req.hdr(Host)`
     - `filter spoe engine metrics-tcp config /usr/local/etc/haproxy/spoe-metrics-tcp.cfg`
     - `default_backend be_mock_backend`
   - `frontend fe_unix`:
     - Binds `0.0.0.0:8081`.
     - `http-request set-var(txn.host) req.hdr(Host)`
     - `filter spoe engine metrics-unix config /usr/local/etc/haproxy/spoe-metrics-unix.cfg`
     - `default_backend be_mock_backend`
   - `backend be_mock_backend`:
     - `mode http`
     - `server s1 mock-backend:8080 check`
   - `backend spoe-metrics-tcp-backend`:
     - `mode tcp`
     - `server spoa_tcp spoa-tcp:9100 check`
   - `backend spoe-metrics-unix-backend`:
     - `mode tcp`
     - `server spoa_unix /var/run/haproxy/spoa.sock check`

### 4.4 Docker Compose Topology (`test/e2e/docker-compose.e2e.yml`)

- **`mock-backend`**:
  - Built from target `mock-backend`.
  - Exposes port `8080` internally on network `e2e_net`.
- **`spoa-tcp`**:
  - Built from target `daemon`.
  - Command: `["--spoe.listen=tcp://0.0.0.0:9100", "--metrics.listen=0.0.0.0:9101", "--max-hosts=10"]`
  - Port mapped: `19100:9101`.
- **`spoa-unix`**:
  - Built from target `daemon`.
  - Command: `["--spoe.listen=unix:///var/run/haproxy/spoa.sock", "--metrics.listen=0.0.0.0:9101", "--max-hosts=10"]`
  - Mounts volume `spoa_socket:/var/run/haproxy`.
  - Port mapped: `19101:9101`.
- **`haproxy`**:
  - Image: `haproxy:2.8-alpine`.
  - Mounts configuration files into `/usr/local/etc/haproxy/`.
  - Mounts volume `spoa_socket:/var/run/haproxy`.
  - Port mapped:
    - `18080:8080` (TCP frontend).
    - `18081:8081` (UNIX frontend).
- **Volumes**:
  - `spoa_socket`: shared between `spoa-unix` and `haproxy`.

---

## 5. Go E2E Test Suite (`test/e2e/e2e_test.go`)

### 5.1 Test Lifecycle (`TestMain`)
1. Checks for availability of `docker` and `docker compose` binaries via `exec.LookPath`.
2. Starts the containers using `docker compose -f test/e2e/docker-compose.e2e.yml up -d --build`.
3. Defers execution of `docker compose -f test/e2e/docker-compose.e2e.yml down -v` to ensure clean environment exit.
4. Executes a readiness probe polling with a 30s timeout:
   - `GET http://localhost:19100/healthz` (200 OK)
   - `GET http://localhost:19101/healthz` (200 OK)
   - `GET http://localhost:18080/healthz` (200 OK via HAProxy to mock backend)
   - `GET http://localhost:18081/healthz` (200 OK via HAProxy to mock backend)
5. Invokes `m.Run()`.
6. Executes teardown.

### 5.2 Test Scenarios & Assertions

1. **`TestE2E_TCP_BasicFlow`**:
   - Sends 5 HTTP GET requests to `http://localhost:18080/` with header `Host: service-a.example.com`.
   - Scrapes `http://localhost:19100/metrics`.
   - Parses Prometheus text format via `prometheus/common/expfmt`.
   - Asserts:
     - `haproxy_host_http_requests_total{host="service-a.example.com", code="200", method="GET"} == 5`.
     - `haproxy_host_http_request_bytes_total{host="service-a.example.com"} > 0`.
     - `haproxy_host_http_response_bytes_total{host="service-a.example.com"} > 0`.
     - `haproxy_host_http_request_duration_seconds_count{host="service-a.example.com"} == 5`.

2. **`TestE2E_UnixSocket_BasicFlow`**:
   - Sends 5 HTTP GET requests to `http://localhost:18081/` with header `Host: service-b.example.com`.
   - Scrapes `http://localhost:19101/metrics`.
   - Asserts:
     - `haproxy_host_http_requests_total{host="service-b.example.com", code="200", method="GET"} == 5`.

3. **`TestE2E_HostNormalization`**:
   - Sends requests through HAProxy (`18080`) with varied Host headers:
     - `billing.test.org:8443` -> metric `host="billing.test.org"`
     - `192.168.1.100:80` -> metric `host="_ipv4_"`
     - `[2001:db8::1]:443` -> metric `host="_ipv6_"`
     - `UPPERCASE.TEST.ORG` -> metric `host="uppercase.test.org"`
     - `trailing.dot.org.` -> metric `host="trailing.dot.org"`
   - Verifies each normalized host label in the Prometheus output.

4. **`TestE2E_CardinalityOverflow`**:
   - Target SPOA runs with `--max-hosts=10`.
   - Sends requests for 15 distinct hosts (`tenant-1.domain.com` through `tenant-15.domain.com`).
   - Asserts:
     - Initial hosts are tracked under their respective host label.
     - Excess hosts are tracked under `host="_overflow_"`.
     - Gauge `haproxy_host_tracked_total == 10`.
     - Gauge `haproxy_host_overflow_total == 1`.

5. **`TestE2E_StatusCodesAndMethods`**:
   - Sends `GET http://localhost:18080/status/404` with `Host: error-app.com`.
   - Sends `POST http://localhost:18080/status/500` with `Host: error-app.com`.
   - Asserts `haproxy_host_http_requests_total{host="error-app.com", code="404", method="GET"} == 1`.
   - Asserts `haproxy_host_http_requests_total{host="error-app.com", code="500", method="POST"} == 1`.

6. **`TestE2E_LatencyHistogram`**:
   - Sends request to `http://localhost:18080/delay/50` with `Host: delay-app.com`.
   - Verifies duration histogram registers bucket counts for latency >= 0.05s.

---

## 6. Makefile Targets

The root `Makefile` will provide:
- `build`: Compiles the binary to `bin/haproxy-metrics-spoa` with version metadata ldflags.
- `test`: Executes standard unit tests (`go test -v ./...`).
- `test-race`: Executes unit tests with race detection (`go test -race -v ./...`).
- `test-e2e`: Runs E2E tests with build tags (`go test -tags=e2e -v -timeout=120s ./test/e2e/...`).
- `docker-build`: Builds local Docker image (`haproxy-metrics-spoa:latest`).
- `docker-e2e-up`: Starts the E2E compose stack in the background.
- `docker-e2e-down`: Stops and cleans up the E2E compose stack and volumes.
- `docker-e2e-logs`: Follows logs from all E2E containers.
- `fmt`: Runs `go fmt ./...`.
- `vet`: Runs `go vet ./...`.
- `clean`: Removes `bin/` directory and orphaned test containers/volumes.

---

## 7. Resilience & Error Handling

1. **Deterministic Cleanup:** The Go test suite handles `SIGINT`/`SIGTERM` and relies on `defer` in `TestMain` to ensure `docker compose down -v` is always called even if assertions fail.
2. **Socket Permissions:** The Docker volume for UNIX socket (`/var/run/haproxy`) has directory permissions `0777` configured in the daemon Dockerfile so the non-root HAProxy process can read and write the UNIX socket.
3. **Port Collision Prevention:** High-range ports (`18080`, `18081`, `19100`, `19101`) are used to avoid conflicting with standard local dev ports (8080, 9100, 9101).
