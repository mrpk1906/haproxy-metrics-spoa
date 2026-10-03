package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCLIVersionFlag(t *testing.T) {
	cmd := exec.Command("go", "run", "main.go", "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run failed: %v, output: %s", err, string(out))
	}
	if !strings.Contains(string(out), "haproxy-metrics-spoa") {
		t.Fatalf("expected version output, got: %s", string(out))
	}
}

func TestCLIHelpFlag(t *testing.T) {
	cmd := exec.Command("go", "run", "main.go", "-h")
	out, _ := cmd.CombinedOutput()
	outputStr := string(out)

	expectedFlags := []string{
		"-spoe.listen",
		"-spoe.socket-mode",
		"-metrics.listen",
		"-metrics.path",
		"-cardinality.max-hosts",
		"-cardinality.group-ips",
		"-version",
	}

	for _, flagName := range expectedFlags {
		if !strings.Contains(outputStr, flagName) {
			t.Errorf("expected flag %s in help output, got: %s", flagName, outputStr)
		}
	}
}

func TestGetEnv(t *testing.T) {
	const testKey = "TEST_SPOA_ENV_VAR"
	os.Unsetenv(testKey)

	// Default fallback
	if val := getEnv(testKey, "default_val"); val != "default_val" {
		t.Errorf("expected default_val, got %s", val)
	}

	// Environment variable set
	os.Setenv(testKey, "custom_val")
	defer os.Unsetenv(testKey)

	if val := getEnv(testKey, "default_val"); val != "custom_val" {
		t.Errorf("expected custom_val, got %s", val)
	}
}
