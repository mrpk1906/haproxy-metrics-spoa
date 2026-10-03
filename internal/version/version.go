package version

import "fmt"

var (
	Version   = "dev"
	GitCommit = "none"
	BuildDate = "unknown"
)

func Info() string {
	if GitCommit != "none" || BuildDate != "unknown" {
		return fmt.Sprintf("haproxy-metrics-spoa %s (commit: %s, date: %s)", Version, GitCommit, BuildDate)
	}
	return fmt.Sprintf("haproxy-metrics-spoa %s", Version)
}
