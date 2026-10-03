package normalizer

import (
	"fmt"
	"sync"
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
		{"port only", ":80", "_other_"},
		{"dot only", ".", "_other_"},
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

func TestConcurrentAccess(t *testing.T) {
	guard := NewGuard(Config{
		MaxTrackedHosts: 50,
		GroupIPs:        true,
	})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				host := fmt.Sprintf("worker%d-site%d.example.com", workerID, j)
				_ = guard.Normalize(host)
				_ = guard.TrackedCount()
			}
		}(i)
	}
	wg.Wait()

	if count := guard.TrackedCount(); count > 50 {
		t.Fatalf("tracked count %d exceeded max 50", count)
	}
}
