//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

const (
	composeFile    = "docker-compose.e2e.yml"
	haproxyTCP     = "http://127.0.0.1:18080"
	haproxyUnix    = "http://127.0.0.1:18081"
	spoaTCPMetric  = "http://127.0.0.1:19100"
	spoaUnixMetric = "http://127.0.0.1:19101"
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

	parser := expfmt.NewTextParser(model.NameValidationScheme)
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
		{"192.168.1.100:80", "_ip_"},
		{"[2001:db8::1]:443", "_ip_"},
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
					// Sample sum should be at least ~0.040s
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

func TestE2E_CardinalityOverflow(t *testing.T) {
	// The daemon runs with --cardinality.max-hosts=10. Send requests to 15 unique hosts.
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

	trackedTotal, ok := findMetricValue(metrics, "haproxy_spoa_tracked_hosts_total", nil)
	if !ok {
		trackedTotal, ok = findMetricValue(metrics, "haproxy_host_tracked_total", nil)
	}
	if !ok || trackedTotal != 10 {
		t.Fatalf("expected tracked hosts to equal 10, got %v", trackedTotal)
	}

	if overflowGauge, ok := findMetricValue(metrics, "haproxy_host_overflow_total", nil); ok {
		if overflowGauge < 1 {
			t.Fatalf("expected haproxy_host_overflow_total gauge >= 1, got %v", overflowGauge)
		}
	}
}
