package version

import (
	"testing"
)

func TestVersionInfo(t *testing.T) {
	Version = "0.1.0"
	GitCommit = "none"
	BuildDate = "unknown"
	expected := "haproxy-metrics-spoa 0.1.0"
	if got := Info(); got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestVersionInfoWithMetadata(t *testing.T) {
	Version = "1.0.0"
	GitCommit = "abc1234"
	BuildDate = "2026-10-03T12:00:00Z"
	expected := "haproxy-metrics-spoa 1.0.0 (commit: abc1234, date: 2026-10-03T12:00:00Z)"
	if got := Info(); got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}
