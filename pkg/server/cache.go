package server

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultToolCacheTTL = 60 * time.Second

type toolCache struct {
	mu      sync.RWMutex
	ttl     time.Duration
	entries map[string]cacheEntry
}

type cacheEntry struct {
	value string
	until time.Time
}

func newToolCache() *toolCache {
	ttl := toolCacheTTLFromEnv()
	if ttl <= 0 {
		return nil
	}
	return &toolCache{
		ttl:     ttl,
		entries: make(map[string]cacheEntry),
	}
}

func toolCacheTTLFromEnv() time.Duration {
	raw := strings.TrimSpace(os.Getenv("UNISTAR_MCP_CACHE_TTL"))
	if raw == "" {
		return defaultToolCacheTTL
	}
	switch strings.ToLower(raw) {
	case "0", "off", "false", "disable", "disabled":
		return 0
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs <= 0 {
		return defaultToolCacheTTL
	}
	return time.Duration(secs) * time.Second
}

func (c *toolCache) get(key string) (string, bool) {
	if c == nil {
		return "", false
	}
	now := time.Now()
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || now.After(entry.until) {
		return "", false
	}
	return entry.value, true
}

func (c *toolCache) set(key, value string) {
	if c == nil || strings.TrimSpace(value) == "" {
		return
	}
	c.mu.Lock()
	c.entries[key] = cacheEntry{
		value: value,
		until: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
}

func cacheKey(tool, repo string, parts ...string) string {
	b := strings.Builder{}
	b.WriteString(tool)
	b.WriteByte('|')
	b.WriteString(repo)
	for _, p := range parts {
		b.WriteByte('|')
		b.WriteString(p)
	}
	return b.String()
}

func (s *Server) cachedString(tool, repo, suffix string, fn func() (string, error)) (string, error) {
	if s.cache == nil {
		return fn()
	}
	key := cacheKey(tool, repo, suffix)
	if v, ok := s.cache.get(key); ok {
		return v, nil
	}
	v, err := fn()
	if err != nil {
		return "", err
	}
	s.cache.set(key, v)
	return v, nil
}
