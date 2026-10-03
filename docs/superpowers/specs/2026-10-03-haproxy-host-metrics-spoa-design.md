# Design Specification: HAProxy Host-Header Metrics SPOA

**Date:** 2026-10-03  
**Status:** Approved for Implementation  
**Project:** `haproxy-metrics-spoa`  
**Target Repository:** `/Users/mrpk1906/Workspace/haproxy-metrics-spoa`

---

## 1. Executive Summary & Motivation

HAProxy's native Prometheus exporter (`http-request use-service prometheus-exporter`) only exports aggregated metrics partitioned by `frontend`, `backend`, and `server`. When an HAProxy frontend terminates traffic across hundreds or thousands of virtual hosts (distinguished via the HTTP `Host` header), HAProxy lacks native per-host visibility (request rates, response codes, latencies, and bandwidth).

`haproxy-metrics-spoa` is a lightweight, high-throughput Stream Processing Offload Agent (SPOA) written in Go. It consumes transaction metadata directly from HAProxy over the binary Stream Processing Offload Protocol (SPOP) using `github.com/dropmorepackets/haproxy-go`, performs host normalization and cardinality protection, and exposes Prometheus RED metrics per virtual host.

---

## 2. Goals & Non-Goals

### Goals
- **Real-Time Host Metrics:** Export Prometheus metrics per virtual host: request count partitioned by HTTP status code and method, latency distributions (histograms), and ingress/egress bytes.
- **Cardinality Explosion Safeguards:** Prevent runaway Prometheus TSDB memory consumption through automatic host normalization, grouping raw IP addresses, and enforcing a configurable maximum tracked hosts threshold (`_overflow_`).
- **Zero Impact on Client Traffic:** Run asynchronously via HAProxy's SPOE engine with a strict execution timeout so slow responses or agent restarts never stall or drop user HTTP requests.
- **Flexible Listener Transports:** Support both UNIX domain sockets (for co-located sidecar deployments with lowest latency) and TCP listeners (for network/containerized deployments).
- **Production-Ready Observability:** Expose internal health and throughput metrics for the SPOA daemon itself.

### Non-Goals
- **Traffic Interception / Modification:** The SPOA does not alter HTTP headers, manipulate routing tables, or block client requests.
- **Log Parsing:** The agent relies exclusively on binary SPOE events from HAProxy, avoiding syslog serialization and text parsing overhead.

---

## 3. High-Level Architecture & SPOE Protocol Flow

```
   Client HTTP Request
            │
            ▼
┌───────────────────────┐   HTTP Response   ┌───────────────────────┐
│        HAProxy        ├──────────────────►│     Client Browser    │
│                       │                   └───────────────────────┘
│  Frontend: fe_http    │
│  Event: on-http-resp  │
│  Filter: spoe engine  │ (Binary SPOP frames over UNIX socket or TCP)
└───────────┬───────────┘
            │
            ▼
┌───────────────────────────────────────────────────────────────────┐
│                    haproxy-metrics-spoa                           │
│                                                                   │
│  1. SPOP Protocol Handler (github.com/dropmorepackets/haproxy-go) │
│     Decodes SPOP NOTIFY frames into Go struct using spoe tags     │
│                                                                   │
│  2. Host Normalizer & Cardinality Guard                           │
│     - Strips port, lowercases, trims whitespace                   │
│     - Maps raw IPv4/IPv6 hosts to "_ip_"                          │
│     - Caps distinct hosts to threshold (overflow -> "_overflow_") │
│                                                                   │
│  3. Prometheus Metric Registry                                    │
│     - Updates request counters, duration histograms, byte metrics │
│                                                                   │
│  4. HTTP Metrics Server (:9101/metrics)                           │
└─────────────────────────────────┬─────────────────────────────────┘
                                  │ Scrape
                                  ▼
                              Prometheus
```

---

## 4. HAProxy Integration & Configuration

### 4.1 Frontend Configuration (`haproxy.cfg`)
```haproxy
frontend fe_http
    bind :80
    bind :443 ssl crt /etc/ssl/certs/
    mode http

    # Capture Host header into transaction variable
    http-request set-var(txn.host) req.hdr(Host)

    # Attach SPOE engine filter
    filter spoe engine metrics config /etc/haproxy/spoe-metrics.cfg

    default_backend be_app
```

### 4.2 SPOE Engine Configuration (`spoe-metrics.cfg`)
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
    # Support UNIX domain socket (recommended) or TCP:
    server spoa1 /var/run/haproxy/spoa.sock check
    # server spoa1 127.0.0.1:12345 check
```

- `option async` and `timeout processing 50ms`: Ensure HAProxy never blocks or delays HTTP responses if the SPOA daemon is restarting or under load.

---

## 5. SPOA Daemon Component Specifications

### 5.1 SPOP Protocol Engine
- **Library:** `github.com/dropmorepackets/haproxy-go/spop` & `pkg/encoding`.
- **Listener Transport:** Binds using standard Go `net.Listener`:
  - If listener URL is `unix:///path/to/sock`, listens on UNIX domain socket, sets file permissions (e.g. `0660`), and removes stale socket files on startup/shutdown.
  - If listener URL is `tcp://host:port`, listens on TCP.
- **Event Unmarshaling:** Uses struct tags for zero-copy parsing:
  ```go
  type HTTPMetricEvent struct {
      Host     string `spoe:"host"`
      Method   string `spoe:"method"`
      Status   int64  `spoe:"status"`
      Latency  int64  `spoe:"lat"`
      ReqBytes int64  `spoe:"req_bytes"`
      ResBytes int64  `spoe:"res_bytes"`
  }
  ```
- **Handler Implementation:**
  ```go
  func (h *Handler) HandleSPOE(ctx context.Context, w *encoding.ActionWriter, m *encoding.Message) {
      if !m.NameEquals("http-response-metric") {
          return
      }
      var evt HTTPMetricEvent
      if err := m.KV.Unmarshal(&evt); err != nil {
          h.metrics.RecordDrop("malformed_message")
          return
      }
      h.collector.RecordEvent(evt)
  }
  ```

### 5.2 Host Normalizer & Cardinality Guard
1. **Sanitization:**
   - Strips `:port` if present (`example.com:8443` $\rightarrow$ `example.com`).
   - Converts host string to lowercase and trims surrounding whitespace or trailing dots.
2. **IP & Scanner Filtering:**
   - Detects if host parses as an IPv4 or IPv6 address (`netip.ParseAddr`). If true, maps host to `_ip_`.
   - If host is empty or contains non-DNS characters (spaces, semicolons), maps to `_other_`.
3. **Cardinality Limiter (Thread-Safe):**
   - Configurable `max_tracked_hosts` (default: 5,000).
   - An in-memory concurrent set/map tracks observed distinct hosts.
   - When set size reaches `max_tracked_hosts`, any new, previously unseen host is labeled as `_overflow_`.

### 5.3 Prometheus Metric Registry
Exposed on HTTP endpoint (default `:9101/metrics`):

| Metric Name | Type | Labels | Description |
|---|---|---|---|
| `haproxy_host_http_requests_total` | Counter | `host`, `code`, `method` | Total HTTP requests per virtual host |
| `haproxy_host_http_request_duration_seconds` | Histogram | `host` | Latency distribution (buckets: 5ms to 10s) |
| `haproxy_host_http_request_bytes_total` | Counter | `host` | Total incoming request payload bytes |
| `haproxy_host_http_response_bytes_total` | Counter | `host` | Total outgoing response payload bytes |
| `haproxy_spoa_messages_received_total` | Counter | `status` (`ok`, `malformed`, `dropped`) | Total SPOP messages processed by the agent |
| `haproxy_spoa_tracked_hosts_total` | Gauge | None | Number of distinct hosts actively tracked |

---

## 6. Configuration & CLI Parameters

Configuration is supplied via CLI flags and matching environment variables:

| CLI Flag | Environment Variable | Default | Description |
|---|---|---|---|
| `--spoe.listen` | `SPOA_LISTEN` | `unix:///var/run/haproxy/spoa.sock` | SPOE listen address (`unix://` or `tcp://`) |
| `--spoe.socket-mode` | `SPOA_SOCKET_MODE` | `0660` | File permissions for UNIX domain socket |
| `--metrics.listen` | `METRICS_LISTEN` | `:9101` | HTTP address for Prometheus metrics |
| `--metrics.path` | `METRICS_PATH` | `/metrics` | HTTP path for metrics endpoint |
| `--cardinality.max-hosts` | `CARDINALITY_MAX_HOSTS` | `5000` | Maximum distinct host labels tracked |
| `--cardinality.group-ips` | `CARDINALITY_GROUP_IPS` | `true` | Group raw IPv4/IPv6 hosts into `_ip_` |
| `--log.level` | `LOG_LEVEL` | `info` | Log verbosity (`debug`, `info`, `warn`, `error`) |

---

## 7. Error Handling, Concurrency & Resilience

- **HAProxy Worker Isolation:** Operates fully concurrently across HAProxy's multi-threaded workers (`nbthread`).
- **Memory Safety:** Uses sync pools provided by `github.com/dropmorepackets/haproxy-go` to minimize heap allocations.
- **Graceful Shutdown:** Intercepts `SIGINT` and `SIGTERM`:
  1. Stops accepting new SPOP connections.
  2. Unlinks the UNIX socket file.
  3. Drains in-flight requests with a 5-second shutdown timeout.
  4. Shuts down the HTTP metrics server.

---

## 8. Verification & Testing Strategy

1. **Unit Tests:**
   - Normalizer: verifies stripping ports, lowercase conversions, IPv4/IPv6 classification, and malformed hosts.
   - Cardinality Guard: verifies bounded memory growth and `_overflow_` label assignment past threshold.
   - Event Handler: verifies decoding and label assignment logic.
2. **Integration Tests:**
   - Mock SPOP Client: automated Go test creating a socket connection, sending synthetic SPOP NOTIFY frames, and asserting Prometheus `/metrics` values.
3. **End-to-End Environment:**
   - Docker Compose containing HAProxy, `haproxy-metrics-spoa`, and Prometheus to verify end-to-end functionality with live HTTP traffic.
