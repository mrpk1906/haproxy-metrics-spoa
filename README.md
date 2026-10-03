# HAProxy Host Metrics SPOA (`haproxy-metrics-spoa`)

A high-performance Stream Processing Offload Agent (SPOA) for HAProxy that collects, normalizes, and exports per-virtual-host HTTP RED (Rate, Errors, Duration) metrics and bandwidth statistics to Prometheus.

---

## Overview

HAProxy provides comprehensive built-in metrics via its native Prometheus exporter (`http-request use-service prometheus-exporter`). However, these metrics are aggregated by frontend, backend, and server. When an HAProxy frontend terminates traffic for hundreds or thousands of virtual hosts (distinguished via the HTTP `Host` header), HAProxy lacks native per-host visibility.

`haproxy-metrics-spoa` solves this by:
- Receiving request/response metadata directly from HAProxy via binary Stream Processing Offload Protocol (SPOP) events.
- Operating asynchronously (`option async`) with strict timeouts so client HTTP traffic is never delayed or blocked.
- Normalizing hostnames and enforcing configurable cardinality safeguards to protect Prometheus TSDB memory.
- Exposing per-host RED metrics and bandwidth counters over a standard Prometheus `/metrics` HTTP endpoint.

---

## Architecture

```
                    Client HTTP Request
                             │
                             ▼
┌────────────────────────────────────────────────────────┐
│                        HAProxy                         │
│                                                        │
│  1. Frontend: fe_http                                  │
│     Captures Host header into transaction variable:    │
│     http-request set-var(txn.host) req.hdr(Host)       │
│                                                        │
│  2. Event: on-http-response                            │
│     SPOE engine sends binary SPOP event asynchronously │
│     filter spoe engine metrics ...                     │
└────────────────────────────┬───────────────────────────┘
                             │
                             │ Binary SPOP over UNIX socket or TCP
                             ▼
┌────────────────────────────────────────────────────────┐
│                  haproxy-metrics-spoa                  │
│                                                        │
│  ┌──────────────────────────────────────────────────┐  │
│  │ SPOP Protocol Handler (dropmorepackets/haproxy-go)│  │
│  └──────────────────────────┬───────────────────────┘  │
│                             │                          │
│  ┌──────────────────────────▼───────────────────────┐  │
│  │ Host Normalizer & Cardinality Guard              │  │
│  │ - Port stripping (:80, :443)                     │  │
│  │ - Lowercase conversion & whitespace trimming     │  │
│  │ - IP address grouping -> "_ip_"                  │  │
│  │ - Invalid host fallback -> "_other_"             │  │
│  │ - Threshold overflow protection -> "_overflow_"  │  │
│  └──────────────────────────┬───────────────────────┘  │
│                             │                          │
│  ┌──────────────────────────▼───────────────────────┐  │
│  │ Prometheus Metrics Collector                     │  │
│  │ - Requests total (host, code, method)            │  │
│  │ - Duration histogram (host)                      │  │
│  │ - Request / response payload bytes (host)        │  │
│  └──────────────────────────┬───────────────────────┘  │
│                             │                          │
│  ┌──────────────────────────▼───────────────────────┐  │
│  │ HTTP Server (:9101/metrics & :9101/healthz)      │  │
│  └──────────────────────────────────────────────────┘  │
└────────────────────────────┬───────────────────────────┘
                             │
                             │ Scrape :9101/metrics
                             ▼
                        Prometheus
```

---

## Features

- **Per-Host RED Observability**:
  - **Rate & Errors**: Request counters partitioned by `host`, HTTP status `code`, and HTTP `method`.
  - **Duration**: Request latency histograms per `host` with buckets from 5ms to 10s.
  - **Payload Bytes**: Ingress and egress payload byte counters per `host`.
- **Built-in Cardinality Explosion Protection**:
  - Automatically strips port numbers (`example.com:443` $\rightarrow$ `example.com`), converts to lowercase, and trims trailing dots.
  - Groups raw IPv4 and IPv6 scanners/bots into a synthetic `_ip_` label.
  - Groups malformed or non-DNS host headers into `_other_`.
  - Limits distinct tracked hosts up to `--cardinality.max-hosts` (default: `5000`). Any newly observed hosts past this limit are mapped to `_overflow_`.
- **Zero Client Latency Overhead**:
  - Operates over HAProxy's asynchronous SPOE engine (`option async`) with a strict processing timeout (e.g. 50ms). If the agent restarts or is unreachable, HAProxy completes client transactions without interruption.
- **Flexible Transports**:
  - UNIX Domain Socket (`unix:///var/run/haproxy/spoa.sock`) for high-throughput, low-latency co-located deployments with configurable file permissions (`--spoe.socket-mode`).
  - TCP Socket (`tcp://127.0.0.1:12345`) for containerized or network-separated topologies.
- **Production-Ready Daemon Operations**:
  - Clean graceful shutdown on `SIGINT`/`SIGTERM` with socket unlinking and in-flight request draining.
  - Liveness/readiness endpoint at `/healthz`.
  - Internal daemon metrics tracking SPOP message processing status (`ok`, `malformed`, `ignored`) and active host counts.

---

## Prometheus Metrics

The following metrics are exposed at `http://<metrics.listen>/metrics`:

| Metric Name | Type | Labels | Description |
|---|---|---|---|
| `haproxy_host_http_requests_total` | Counter | `host`, `code`, `method` | Total HTTP requests partitioned by virtual host, HTTP response status code, and HTTP method. |
| `haproxy_host_http_request_duration_seconds` | Histogram | `host` | Latency distribution of HTTP requests per virtual host. Buckets: `[0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0]`. |
| `haproxy_host_http_request_bytes_total` | Counter | `host` | Total incoming HTTP request payload bytes received per virtual host. |
| `haproxy_host_http_response_bytes_total` | Counter | `host` | Total outgoing HTTP response payload bytes transmitted per virtual host. |
| `haproxy_spoa_messages_received_total` | Counter | `status` | Total SPOP messages processed by the agent. Status values: `ok`, `malformed`, `ignored`. |
| `haproxy_spoa_tracked_hosts_total` | Gauge | _(none)_ | Current count of unique virtual hosts actively tracked in memory by the cardinality guard. |

In addition, standard Go runtime and process metrics (`go_*`, `process_*`) are registered and exported.

---

## CLI Flags & Environment Variables

`haproxy-metrics-spoa` can be configured via CLI flags or matching environment variables:

| Flag | Environment Variable | Default | Description |
|---|---|---|---|
| `--spoe.listen` | `SPOA_LISTEN` | `unix:///var/run/haproxy/spoa.sock` | SPOE listen address (`unix:///path/to/sock` or `tcp://host:port`). |
| `--spoe.socket-mode` | `SPOA_SOCKET_MODE` | `0660` (`432` dec) | File permissions for UNIX domain socket. |
| `--metrics.listen` | `METRICS_LISTEN` | `:9101` | TCP address to serve Prometheus HTTP metrics. |
| `--metrics.path` | `METRICS_PATH` | `/metrics` | HTTP path where Prometheus metrics are exposed. |
| `--cardinality.max-hosts` | `CARDINALITY_MAX_HOSTS` | `5000` | Maximum distinct host labels tracked before routing new hosts to `_overflow_`. |
| `--cardinality.group-ips` | `CARDINALITY_GROUP_IPS` | `true` | When `true`, raw IPv4 and IPv6 hosts are categorized into `_ip_`. |
| `--version` | _(none)_ | `false` | Print version and build information, then exit. |
| `-h`, `--help` | _(none)_ | `false` | Print CLI usage instructions and exit. |

---

## HAProxy Configuration

Production configuration templates are located in [`examples/haproxy/`](examples/haproxy/).

### 1. SPOE Filter Configuration (`examples/haproxy/spoe-metrics.cfg`)

Place this file at `/etc/haproxy/spoe-metrics.cfg`:

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
```

Key settings:
- **`option async`**: Allows HAProxy to offload SPOP processing asynchronously without pausing HTTP request/response loops.
- **`timeout processing 50ms`**: Bounds offload processing time; HAProxy continues serving traffic if the agent does not respond within this window.
- **`args`**: Transmits Host (`var(txn.host)`), HTTP method, status code, latency in milliseconds, and request/response payload lengths.

### 2. Main HAProxy Configuration (`examples/haproxy/haproxy.cfg`)

Include the variable capture, SPOE filter in your frontend, and the SPOE backend:

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

    # 1. Capture the Host header into a transaction variable
    http-request set-var(txn.host) req.hdr(Host)

    # 2. Attach SPOE metrics filter
    filter spoe engine metrics config /etc/haproxy/spoe-metrics.cfg

    default_backend be_app

backend be_app
    mode http
    server s1 127.0.0.1:8080 check

backend spoe-metrics-backend
    mode tcp
    server spoa1 /var/run/haproxy/spoa.sock check
    # For TCP transport:
    # server spoa1 127.0.0.1:9100 check
```

---

## Installation & Getting Started

### Prerequisites

- Go 1.27 or newer
- HAProxy 2.0+ (compiled with SPOE support)

### Building from Source

```bash
git clone https://github.com/mrpk1906/haproxy-metrics-spoa.git
cd haproxy-metrics-spoa

# Build binary
go build -o bin/haproxy-metrics-spoa ./cmd/spoa
```

To build with embedded release metadata:

```bash
VERSION="1.0.0"
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

go build -ldflags "-X github.com/mrpk1906/haproxy-metrics-spoa/internal/version.Version=${VERSION} \
                   -X github.com/mrpk1906/haproxy-metrics-spoa/internal/version.GitCommit=${COMMIT} \
                   -X github.com/mrpk1906/haproxy-metrics-spoa/internal/version.BuildDate=${DATE}" \
         -o bin/haproxy-metrics-spoa ./cmd/spoa
```

### Running the Daemon

#### Using UNIX Domain Socket (Default)

```bash
# Ensure socket directory exists with proper permissions for HAProxy
sudo mkdir -p /var/run/haproxy
sudo chown haproxy:haproxy /var/run/haproxy

# Run the SPOA agent
./bin/haproxy-metrics-spoa \
  --spoe.listen="unix:///var/run/haproxy/spoa.sock" \
  --spoe.socket-mode=0660 \
  --metrics.listen=":9101"
```

#### Using TCP Transport

```bash
./bin/haproxy-metrics-spoa \
  --spoe.listen="tcp://127.0.0.1:9100" \
  --metrics.listen=":9101"
```

### Health & Metrics Verification

Verify daemon health:
```bash
curl http://localhost:9101/healthz
# Output: OK
```

Inspect Prometheus metrics:
```bash
curl http://localhost:9101/metrics
```

---

## Prometheus Scrape Configuration

Add the following scrape configuration to your `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: "haproxy_host_metrics"
    scrape_interval: 10s
    static_configs:
      - targets: ["127.0.0.1:9101"]
        labels:
          instance: "haproxy-node-01"
```

---

## Running Tests

Run the full automated test suite:

```bash
go test -v ./...
```

Run tests with race detection:

```bash
go test -race -v ./...
```

---

## License

This project is licensed under the Apache 2.0 License.
