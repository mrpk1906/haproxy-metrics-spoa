## Project Overview
`haproxy-metrics-spoa` is a high-performance Stream Processing Offload Agent (SPOA) daemon for HAProxy that extracts per-virtual-host HTTP traffic metrics via the Stream Processing Offload Protocol (SPOP) and exports Prometheus RED metrics (request count, duration histograms, request/response payload bytes). It features built-in host normalization and thread-safe cardinality protection to guard Prometheus registries against high-cardinality label explosion caused by malicious bot scans or domain spoofing.
- Core tech stack: Go 1.27.0+, standard library (`net`, `net/http`, `sync`, `sync/atomic`).
- Key dependencies: `github.com/dropmorepackets/haproxy-go` (v0.1.1, SPOP protocol engine & binary encoding), `github.com/prometheus/client_golang` (v1.24.1, Prometheus client).
- Architecture: Pure Go, zero CGo dependencies.

## Core Commands
- Run all tests: `go test -v ./...`
- Run tests with race detection: `go test -race -v ./...`
- Run specific package tests: `go test -v ./pkg/normalizer` (or `./pkg/metrics`, `./pkg/spoa`, `./pkg/server`, `./cmd/spoa`)
- Build daemon binary: `go build -o bin/haproxy-metrics-spoa ./cmd/spoa`
- Build with version ldflags:
  ```bash
  go build -ldflags "-X github.com/mrpk1906/haproxy-metrics-spoa/internal/version.Version=1.0.0 \
                     -X github.com/mrpk1906/haproxy-metrics-spoa/internal/version.GitCommit=$(git rev-parse --short HEAD) \
                     -X github.com/mrpk1906/haproxy-metrics-spoa/internal/version.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
           -o bin/haproxy-metrics-spoa ./cmd/spoa
  ```
- Run locally with UNIX socket:
  ```bash
  ./bin/haproxy-metrics-spoa --spoe.listen="unix:///tmp/spoa.sock" --metrics.listen="127.0.0.1:9101"
  ```
- Run locally with TCP socket:
  ```bash
  ./bin/haproxy-metrics-spoa --spoe.listen="tcp://127.0.0.1:9100" --metrics.listen="127.0.0.1:9101"
  ```
- Format code: `go fmt ./...`
- Tidy dependencies: `go mod tidy`

## Repository Structure
- `cmd/spoa/`: Application entrypoint (`main.go`, `main_test.go`), CLI flag parsing, environment variable resolution, signal handling (`SIGINT`, `SIGTERM`), and server lifecycle management.
- `pkg/normalizer/`: Hostname sanitization (port stripping, lowercasing, dot trimming, IP detection) and thread-safe cardinality guard (`Guard`) enforcing bounded unique host sets with `_overflow_` fallback.
- `pkg/metrics/`: Prometheus metric definitions (Counters, Histogram, GaugeFunc), RED metric recording (`RecordEvent`), status code formatting, method whitelisting, and configurable latency bucket support.
- `pkg/server/`: Dual-listener network server (`Server`), managing the SPOE listener (UNIX socket or TCP via `spop.Agent`), HTTP Prometheus metrics endpoint (`/metrics`), `/healthz` endpoint, and graceful draining.
- `pkg/spoa/`: SPOE message handler (`Handler`) implementing `spop.Handler`, unmarshaling binary `http-response-metric` NOTIFY frames into `metrics.HTTPMetricEvent`.
- `internal/version/`: Build and release metadata (`Version`, `GitCommit`, `BuildDate`).
- `examples/haproxy/`: Production configuration examples (`haproxy.cfg`, `spoe-metrics.cfg`) for integrating HAProxy with the SPOA daemon.
- `docs/superpowers/`: Architecture specifications and implementation design plans.

## Code Style & Conventions
- **Go Standards:** Strict adherence to idiomatic Go (`gofmt`, standard formatting, simple control flow, standard library first).
- **Error Handling:** Explicit error checking and propagation using `fmt.Errorf("...: %w", err)`. Never swallow errors or use blank identifiers without clear comment justification.
- **Concurrency & Synchronization:**
  - Protect shared state with `sync.RWMutex` (read lock fast paths, write locks for updates/insertions).
  - Use `sync/atomic` or read locks for gauge metric callbacks (`prometheus.GaugeFunc`).
  - Pass `context.Context` for cancellation and bounded timeouts across server lifecycle methods.
- **Resource Lifecycle:**
  - Ensure UNIX sockets created by tests or runtime are cleanly unlinked on shutdown and exit.
  - Release pooled SPOP messages (`encoding.ReleaseMessage`, `encoding.ReleaseKVScanner`) via `defer`.
- **Naming Conventions:**
  - CamelCase for Go identifiers (`HTTPMetricEvent`, `SPOEListen`).
  - Snake_case for SPOE frame argument names (`res_bytes`, `req_bytes`, `txn.host`).
  - Prometheus metric names follow official conventions: `<namespace>_<subsystem>_<name>_<unit>` (e.g. `haproxy_host_http_requests_total`).

## Operational Boundaries & Guardrails
- **Zero CGo Dependency:** Maintain pure Go compatibility across all packages; never introduce CGo or native library bindings.
- **Zero Client Latency Overhead:** All SPOP handling must remain non-blocking and in-memory; never execute slow disk I/O, external network calls, or blocking locks on the SPOE message path.
- **Cardinality Explosion Guard:** Never expose raw, unvalidated host headers directly to Prometheus metrics; all hostnames MUST pass through `normalizer.Guard.Normalize()`.
- **Test Integrity:** Always run `go test -v ./...` and `go test -race ./...` before declaring any task complete. Tests in `pkg/server/server_test.go` bind local sockets and require appropriate execution permissions.
- **SPOP Protocol Compatibility:** Keep SPOE message name (`http-response-metric`) and argument keys synchronized between `pkg/spoa/handler.go`, `pkg/metrics/collector.go`, and `examples/haproxy/spoe-metrics.cfg`.
