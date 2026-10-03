package metrics

import (
	"sync"
	"testing"

	"github.com/mrpk1906/haproxy-metrics-spoa/pkg/normalizer"
	"github.com/prometheus/client_golang/prometheus"
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
	foundDuration := false
	foundReqBytes := false
	foundResBytes := false
	foundMessages := false
	foundTrackedHosts := false

	for _, mf := range metricFamilies {
		switch mf.GetName() {
		case "haproxy_host_http_requests_total":
			foundRequests = true
			if len(mf.Metric) != 1 {
				t.Fatalf("expected 1 metric for requests_total, got %d", len(mf.Metric))
			}
			m := mf.Metric[0]
			var hostLabel, codeLabel, methodLabel string
			for _, p := range m.Label {
				switch p.GetName() {
				case "host":
					hostLabel = p.GetValue()
				case "code":
					codeLabel = p.GetValue()
				case "method":
					methodLabel = p.GetValue()
				}
			}
			if hostLabel != "api.example.com" {
				t.Errorf("expected host 'api.example.com', got %q", hostLabel)
			}
			if codeLabel != "200" {
				t.Errorf("expected code '200', got %q", codeLabel)
			}
			if methodLabel != "GET" {
				t.Errorf("expected method 'GET', got %q", methodLabel)
			}
			if m.GetCounter().GetValue() != 1 {
				t.Errorf("expected count 1, got %f", m.GetCounter().GetValue())
			}

		case "haproxy_host_http_request_duration_seconds":
			foundDuration = true
			if len(mf.Metric) != 1 {
				t.Fatalf("expected 1 metric for duration_seconds, got %d", len(mf.Metric))
			}
			m := mf.Metric[0]
			h := m.GetHistogram()
			if h.GetSampleCount() != 1 {
				t.Errorf("expected 1 sample, got %d", h.GetSampleCount())
			}
			if h.GetSampleSum() < 0.124 || h.GetSampleSum() > 0.126 {
				t.Errorf("expected sample sum approx 0.125s, got %f", h.GetSampleSum())
			}

		case "haproxy_host_http_request_bytes_total":
			foundReqBytes = true
			if len(mf.Metric) != 1 {
				t.Fatalf("expected 1 metric for req_bytes, got %d", len(mf.Metric))
			}
			m := mf.Metric[0]
			if m.GetCounter().GetValue() != 512 {
				t.Errorf("expected 512 req bytes, got %f", m.GetCounter().GetValue())
			}

		case "haproxy_host_http_response_bytes_total":
			foundResBytes = true
			if len(mf.Metric) != 1 {
				t.Fatalf("expected 1 metric for res_bytes, got %d", len(mf.Metric))
			}
			m := mf.Metric[0]
			if m.GetCounter().GetValue() != 2048 {
				t.Errorf("expected 2048 res bytes, got %f", m.GetCounter().GetValue())
			}

		case "haproxy_spoa_messages_received_total":
			foundMessages = true
			if len(mf.Metric) != 1 {
				t.Fatalf("expected 1 metric for messages_total, got %d", len(mf.Metric))
			}
			m := mf.Metric[0]
			if len(m.Label) != 1 || m.Label[0].GetName() != "status" || m.Label[0].GetValue() != "ok" {
				t.Errorf("unexpected labels for messages_total: %v", m.Label)
			}
			if m.GetCounter().GetValue() != 1 {
				t.Errorf("expected count 1, got %f", m.GetCounter().GetValue())
			}

		case "haproxy_spoa_tracked_hosts_total":
			foundTrackedHosts = true
			if len(mf.Metric) != 1 {
				t.Fatalf("expected 1 metric for tracked_hosts, got %d", len(mf.Metric))
			}
			m := mf.Metric[0]
			if m.GetGauge().GetValue() != 1 {
				t.Errorf("expected 1 tracked host, got %f", m.GetGauge().GetValue())
			}
		}
	}

	if !foundRequests {
		t.Error("haproxy_host_http_requests_total metric family not found")
	}
	if !foundDuration {
		t.Error("haproxy_host_http_request_duration_seconds metric family not found")
	}
	if !foundReqBytes {
		t.Error("haproxy_host_http_request_bytes_total metric family not found")
	}
	if !foundResBytes {
		t.Error("haproxy_host_http_response_bytes_total metric family not found")
	}
	if !foundMessages {
		t.Error("haproxy_spoa_messages_received_total metric family not found")
	}
	if !foundTrackedHosts {
		t.Error("haproxy_spoa_tracked_hosts_total metric family not found")
	}
}

func TestCollectorDefaultMethodAndNegativeValues(t *testing.T) {
	guard := normalizer.NewGuard(normalizer.Config{MaxTrackedHosts: 10, GroupIPs: true})
	reg := prometheus.NewRegistry()
	c := NewCollector(guard, reg)

	// Empty method should default to UNKNOWN
	// Latency < 0 should not be observed
	// ReqBytes <= 0 and ResBytes <= 0 should not be added
	c.RecordEvent(HTTPMetricEvent{
		Host:     "test.domain.com",
		Method:   "",
		Status:   500,
		Latency:  -1,
		ReqBytes: 0,
		ResBytes: -10,
	})

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}

	var methodLabel string
	hasDuration := false
	hasReqBytes := false
	hasResBytes := false

	for _, mf := range mfs {
		switch mf.GetName() {
		case "haproxy_host_http_requests_total":
			if len(mf.Metric) > 0 {
				for _, lbl := range mf.Metric[0].Label {
					if lbl.GetName() == "method" {
						methodLabel = lbl.GetValue()
					}
				}
			}
		case "haproxy_host_http_request_duration_seconds":
			if len(mf.Metric) > 0 {
				hasDuration = true
			}
		case "haproxy_host_http_request_bytes_total":
			if len(mf.Metric) > 0 {
				hasReqBytes = true
			}
		case "haproxy_host_http_response_bytes_total":
			if len(mf.Metric) > 0 {
				hasResBytes = true
			}
		}
	}

	if methodLabel != "UNKNOWN" {
		t.Errorf("expected method 'UNKNOWN', got %q", methodLabel)
	}
	if hasDuration {
		t.Errorf("expected no duration histogram recorded for negative latency")
	}
	if hasReqBytes {
		t.Errorf("expected no req bytes recorded for 0 bytes")
	}
	if hasResBytes {
		t.Errorf("expected no res bytes recorded for negative bytes")
	}
}

func TestCollectorConcurrentAccess(t *testing.T) {
	guard := normalizer.NewGuard(normalizer.Config{MaxTrackedHosts: 100, GroupIPs: true})
	reg := prometheus.NewRegistry()
	c := NewCollector(guard, reg)

	var wg sync.WaitGroup
	workers := 10
	iterations := 100

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				c.RecordEvent(HTTPMetricEvent{
					Host:     "api.example.com",
					Method:   "POST",
					Status:   200,
					Latency:  10,
					ReqBytes: 100,
					ResBytes: 200,
				})
				c.RecordMessage("ok")
			}
		}(i)
	}

	wg.Wait()

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}

	totalExpected := float64(workers * iterations)
	for _, mf := range mfs {
		if mf.GetName() == "haproxy_host_http_requests_total" {
			if len(mf.Metric) != 1 || mf.Metric[0].GetCounter().GetValue() != totalExpected {
				t.Errorf("expected requests_total %f, got %f", totalExpected, mf.Metric[0].GetCounter().GetValue())
			}
		}
		if mf.GetName() == "haproxy_spoa_messages_received_total" {
			if len(mf.Metric) != 1 || mf.Metric[0].GetCounter().GetValue() != totalExpected {
				t.Errorf("expected messages_total %f, got %f", totalExpected, mf.Metric[0].GetCounter().GetValue())
			}
		}
	}
}
