package version

import "fmt"

var Version = "dev"

func Info() string {
	return fmt.Sprintf("haproxy-metrics-spoa %s", Version)
}
