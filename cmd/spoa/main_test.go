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
		"-metrics.latency-buckets",
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

func TestParseLatencyBuckets(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []float64
		wantErr bool
	}{
		{
			name:    "empty string defaults to nil",
			input:   "",
			want:    nil,
			wantErr: false,
		},
		{
			name:    "whitespace only defaults to nil",
			input:   "   ",
			want:    nil,
			wantErr: false,
		},
		{
			name:    "valid sorted buckets",
			input:   "0.01, 0.1, 1.0, 30.0, 180.0",
			want:    []float64{0.01, 0.1, 1.0, 30.0, 180.0},
			wantErr: false,
		},
		{
			name:    "invalid float",
			input:   "0.1, invalid, 1.0",
			wantErr: true,
		},
		{
			name:    "zero value bucket",
			input:   "0, 1.0",
			wantErr: true,
		},
		{
			name:    "negative value bucket",
			input:   "-0.5, 1.0",
			wantErr: true,
		},
		{
			name:    "non-increasing buckets",
			input:   "1.0, 0.5",
			wantErr: true,
		},
		{
			name:    "duplicate consecutive buckets",
			input:   "1.0, 1.0",
			wantErr: true,
		},
		{
			name:    "empty item in list",
			input:   "0.1, , 1.0",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseLatencyBuckets(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseLatencyBuckets(%q) err = %v, wantErr = %v", tc.input, err, tc.wantErr)
			}
			if !tc.wantErr {
				if len(got) != len(tc.want) {
					t.Fatalf("parseLatencyBuckets(%q) = %v, want %v", tc.input, got, tc.want)
				}
				for i := range got {
					if got[i] != tc.want[i] {
						t.Errorf("bucket[%d] = %v, want %v", i, got[i], tc.want[i])
					}
				}
			}
		})
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
