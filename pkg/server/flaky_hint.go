package server

import (
	"fmt"
	"strings"
)

// formatFlakyFingerprintHint summarizes webhook-ledger recurrence for a failure FP.
func (s *Server) formatFlakyFingerprintHint(repo, fingerprint string) string {
	if fingerprint == "" {
		return ""
	}
	count := 0
	if s != nil && s.events != nil {
		count = s.events.countByFingerprint(repo, fingerprint)
	}
	switch {
	case count == 0:
		return "Flaky hint: new fingerprint in webhook ledger (or webhook not configured)"
	case count == 1:
		return "Flaky hint: seen once before in webhook ledger — may be flaky"
	default:
		return fmt.Sprintf(
			"Flaky hint: recurring (seen %d times in webhook ledger) — rerun may succeed",
			count,
		)
	}
}

func (es *eventStore) countByFingerprint(repo, fingerprint string) int {
	if es == nil || fingerprint == "" {
		return 0
	}
	es.mu.RLock()
	defer es.mu.RUnlock()
	n := 0
	for _, ev := range es.events {
		if ev.Fingerprint != fingerprint {
			continue
		}
		if repo != "" && !strings.EqualFold(ev.Repo, repo) {
			continue
		}
		n++
	}
	return n
}
