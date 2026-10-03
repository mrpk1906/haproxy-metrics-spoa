package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/haproxy-metrics-spoa/internal/version"
	"github.com/haproxy-metrics-spoa/pkg/metrics"
	"github.com/haproxy-metrics-spoa/pkg/normalizer"
	"github.com/haproxy-metrics-spoa/pkg/server"
	"github.com/haproxy-metrics-spoa/pkg/spoa"
	"github.com/prometheus/client_golang/prometheus"
)

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func main() {
	var (
		spoeListen    = flag.String("spoe.listen", getEnv("SPOA_LISTEN", "unix:///var/run/haproxy/spoa.sock"), "SPOE listen URL (unix:///path or tcp://host:port)")
		socketMode    = flag.Uint("spoe.socket-mode", 0660, "UNIX socket file permission mode")
		metricsListen = flag.String("metrics.listen", getEnv("METRICS_LISTEN", ":9101"), "Address to serve Prometheus metrics")
		metricsPath   = flag.String("metrics.path", getEnv("METRICS_PATH", "/metrics"), "HTTP path for metrics")
		maxHosts      = flag.Int("cardinality.max-hosts", 5000, "Maximum distinct hosts to track before grouping into _overflow_")
		groupIPs      = flag.Bool("cardinality.group-ips", true, "Group raw IPv4/IPv6 hosts into _ip_")
		showVersion   = flag.Bool("version", false, "Print version information and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Info())
		os.Exit(0)
	}

	log.Printf("Starting %s", version.Info())
	log.Printf("Config: SPOE listen=%s, metrics=%s%s, max-hosts=%d, group-ips=%v",
		*spoeListen, *metricsListen, *metricsPath, *maxHosts, *groupIPs)

	guard := normalizer.NewGuard(normalizer.Config{
		MaxTrackedHosts: *maxHosts,
		GroupIPs:        *groupIPs,
	})

	reg := prometheus.DefaultRegisterer
	collector := metrics.NewCollector(guard, reg)
	handler := spoa.NewHandler(collector)

	cfg := server.Config{
		SPOEListen:    *spoeListen,
		SocketMode:    os.FileMode(*socketMode),
		MetricsListen: *metricsListen,
		MetricsPath:   *metricsPath,
	}

	srv, err := server.NewServer(cfg, handler, prometheus.DefaultGatherer.(*prometheus.Registry))
	if err != nil {
		log.Fatalf("Failed to initialize server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Printf("Received signal %s, initiating graceful shutdown...", sig)
		cancel()
	}()

	if err := srv.Start(ctx); err != nil {
		log.Printf("Server stopped with error: %v", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	log.Println("haproxy-metrics-spoa terminated gracefully.")
}
