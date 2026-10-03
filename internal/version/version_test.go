package version

import (
	"testing"
)

func TestVersionInfo(t *testing.T) {
	Version = "0.1.0"
	expected := "haproxy-metrics-spoa 0.1.0"
	if got := Info(); got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}
