package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dropmorepackets/haproxy-go/spop"
	"github.com/mrpk1906/haproxy-metrics-spoa/pkg/spoa"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Config struct {
	SPOEListen    string
	SocketMode    os.FileMode
	MetricsListen string
	MetricsPath   string
}

type Server struct {
	cfg          Config
	handler      *spoa.Handler
	reg          *prometheus.Registry
	spopAgent    *spop.Agent
	spoeListener net.Listener
	httpServer   *http.Server
	metricsAddr  string
	isUnixSock   bool
	unixSockPath string
	shutdownCh   chan struct{}
	shutdownOnce sync.Once
	mu           sync.Mutex
}

func NewServer(cfg Config, handler *spoa.Handler, reg *prometheus.Registry) (*Server, error) {
	if handler == nil {
		return nil, fmt.Errorf("spoa handler cannot be nil")
	}
	if reg == nil {
		return nil, fmt.Errorf("prometheus registry cannot be nil")
	}
	return &Server{
		cfg:        cfg,
		handler:    handler,
		reg:        reg,
		shutdownCh: make(chan struct{}),
	}, nil
}

func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.spoeListener != nil || s.httpServer != nil {
		s.mu.Unlock()
		return fmt.Errorf("server already started")
	}

	// 1. Setup SPOP Listener
	listenURL := s.cfg.SPOEListen
	if !strings.Contains(listenURL, "://") {
		listenURL = "unix://" + listenURL
	}
	u, err := url.Parse(listenURL)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("invalid spoe listen url: %w", err)
	}

	var listener net.Listener
	switch u.Scheme {
	case "unix":
		sockPath := u.Path
		if sockPath == "" {
			sockPath = u.Host
		} else if u.Host != "" {
			sockPath = filepath.Join(u.Host, u.Path)
		}
		s.isUnixSock = true
		s.unixSockPath = sockPath
		if dir := filepath.Dir(sockPath); dir != "" && dir != "." {
			_ = os.MkdirAll(dir, 0755)
		}
		_ = os.Remove(sockPath)
		listener, err = net.Listen("unix", sockPath)
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("listen unix socket error: %w", err)
		}
		mode := s.cfg.SocketMode
		if mode == 0 {
			mode = 0660
		}
		_ = os.Chmod(sockPath, mode)
	case "tcp":
		addr := u.Host
		if addr == "" {
			addr = u.Path
		}
		listener, err = net.Listen("tcp", addr)
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("listen tcp socket error: %w", err)
		}
	default:
		s.mu.Unlock()
		return fmt.Errorf("unsupported scheme %q (expected unix or tcp)", u.Scheme)
	}

	s.spoeListener = listener
	s.spopAgent = &spop.Agent{
		Handler:     spop.HandlerFunc(s.handler.HandleSPOE),
		BaseContext: ctx,
	}

	// 2. Setup HTTP Metrics Server
	mux := http.NewServeMux()
	metricsPath := s.cfg.MetricsPath
	if metricsPath == "" {
		metricsPath = "/metrics"
	}
	mux.Handle(metricsPath, promhttp.HandlerFor(s.reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK\n"))
	})

	httpListener, err := net.Listen("tcp", s.cfg.MetricsListen)
	if err != nil {
		_ = s.spoeListener.Close()
		if s.isUnixSock && s.unixSockPath != "" {
			_ = os.Remove(s.unixSockPath)
		}
		s.mu.Unlock()
		return fmt.Errorf("listen http metrics error: %w", err)
	}

	s.metricsAddr = httpListener.Addr().String()
	s.httpServer = &http.Server{
		Handler: mux,
	}
	s.mu.Unlock()

	errCh := make(chan error, 2)

	go func() {
		if err := s.spopAgent.Serve(listener); err != nil && !errors.Is(err, net.ErrClosed) {
			errCh <- fmt.Errorf("spop agent exited with error: %w", err)
		}
	}()

	go func() {
		if err := s.httpServer.Serve(httpListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http metrics server exited with error: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		return nil
	case <-s.shutdownCh:
		return nil
	case err := <-errCh:
		return err
	}
}

func (s *Server) MetricsAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.httpServer == nil {
		return ""
	}
	if s.metricsAddr != "" {
		return s.metricsAddr
	}
	return s.cfg.MetricsListen
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.shutdownOnce.Do(func() {
		close(s.shutdownCh)
	})

	var errs []string
	if s.spoeListener != nil {
		if err := s.spoeListener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, fmt.Sprintf("close spoe listener: %v", err))
		}
	}
	if s.isUnixSock && s.unixSockPath != "" {
		_ = os.Remove(s.unixSockPath)
		s.unixSockPath = ""
	}
	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Sprintf("shutdown http server: %v", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("shutdown errors: %s", strings.Join(errs, "; "))
	}
	return nil
}
