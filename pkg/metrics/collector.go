package metrics

import (
	"strconv"
	"strings"

	"github.com/mrpk1906/haproxy-metrics-spoa/pkg/normalizer"
	"github.com/prometheus/client_golang/prometheus"
)

var statusStrings [600]string

func init() {
	for i := 100; i < 600; i++ {
		statusStrings[i] = strconv.Itoa(i)
	}
}

func formatStatusCode(code int64) string {
	if code >= 100 && code < 600 {
		return statusStrings[code]
	}
	if code <= 0 {
		return "0"
	}
	return "OTHER"
}

func normalizeMethod(m string) string {
	switch strings.ToUpper(strings.TrimSpace(m)) {
	case "GET":
		return "GET"
	case "POST":
		return "POST"
	case "PUT":
		return "PUT"
	case "DELETE":
		return "DELETE"
	case "HEAD":
		return "HEAD"
	case "OPTIONS":
		return "OPTIONS"
	case "PATCH":
		return "PATCH"
	case "CONNECT":
		return "CONNECT"
	case "TRACE":
		return "TRACE"
	case "":
		return "UNKNOWN"
	default:
		return "OTHER"
	}
}

type HTTPMetricEvent struct {
	Host     string `spoe:"host"`
	Method   string `spoe:"method"`
	Status   int64  `spoe:"status"`
	Latency  int64  `spoe:"lat"`
	ReqBytes int64  `spoe:"req_bytes"`
	ResBytes int64  `spoe:"res_bytes"`
}

var DefaultLatencyBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0, 120.0, 180.0,
}

type Collector struct {
	guard         *normalizer.Guard
	requestsTotal *prometheus.CounterVec
	durationHist  *prometheus.HistogramVec
	reqBytesTotal *prometheus.CounterVec
	resBytesTotal *prometheus.CounterVec
	messagesTotal *prometheus.CounterVec
	trackedHosts  prometheus.GaugeFunc
}

func NewCollector(guard *normalizer.Guard, reg prometheus.Registerer, latencyBuckets []float64) *Collector {
	buckets := latencyBuckets
	if len(buckets) == 0 {
		buckets = DefaultLatencyBuckets
	}

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
				Buckets: buckets,
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
	codeStr := formatStatusCode(evt.Status)
	method := normalizeMethod(evt.Method)

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
