package normalizer

import (
	"net"
	"net/netip"
	"strings"
	"sync"
)

type Config struct {
	MaxTrackedHosts int
	GroupIPs        bool
}

type Guard struct {
	cfg          Config
	mu           sync.RWMutex
	trackedHosts map[string]struct{}
}

func NewGuard(cfg Config) *Guard {
	if cfg.MaxTrackedHosts <= 0 {
		cfg.MaxTrackedHosts = 5000
	}
	return &Guard{
		cfg:          cfg,
		trackedHosts: make(map[string]struct{}),
	}
}

func (g *Guard) Normalize(rawHost string) string {
	cleaned := strings.TrimSpace(rawHost)
	if cleaned == "" {
		return "_other_"
	}

	// Strip IPv6 brackets if present with or without port: [::1]:80 or [::1]
	if strings.HasPrefix(cleaned, "[") {
		if closeIdx := strings.LastIndex(cleaned, "]"); closeIdx != -1 {
			hostPart := cleaned[1:closeIdx]
			cleaned = hostPart
		}
	} else if host, _, err := net.SplitHostPort(cleaned); err == nil {
		cleaned = host
	}

	cleaned = strings.ToLower(strings.TrimSuffix(cleaned, "."))
	if cleaned == "" {
		return "_other_"
	}

	if strings.ContainsAny(cleaned, " \t\r\n/\\") {
		return "_other_"
	}

	if g.cfg.GroupIPs {
		if _, err := netip.ParseAddr(cleaned); err == nil {
			return "_ip_"
		}
	}

	// Check if already tracked
	g.mu.RLock()
	_, exists := g.trackedHosts[cleaned]
	g.mu.RUnlock()
	if exists {
		return cleaned
	}

	// Register new host under write lock
	g.mu.Lock()
	defer g.mu.Unlock()

	// Double-check under write lock
	if _, exists := g.trackedHosts[cleaned]; exists {
		return cleaned
	}

	if len(g.trackedHosts) >= g.cfg.MaxTrackedHosts {
		return "_overflow_"
	}

	g.trackedHosts[cleaned] = struct{}{}
	return cleaned
}

func (g *Guard) TrackedCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.trackedHosts)
}
