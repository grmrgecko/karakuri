package mq

import "strings"

// Match returns true if the subscription pattern matches the given topic.
// Topics use ":" as the segment separator. The wildcard "*" in a pattern
// matches one or more consecutive segments, with backtracking to allow
// remaining pattern segments to match.
func Match(pattern, topic string) bool {
	if pattern == topic {
		return true
	}
	patParts := strings.Split(pattern, ":")
	topParts := strings.Split(topic, ":")
	return matchParts(patParts, 0, topParts, 0)
}

// matchParts recursively matches pattern segments against topic segments.
func matchParts(pat []string, pi int, top []string, ti int) bool {
	for pi < len(pat) && ti < len(top) {
		if pat[pi] == "*" {
			// Try consuming 1..N topic segments for this wildcard.
			for consumed := 1; consumed <= len(top)-ti; consumed++ {
				if matchParts(pat, pi+1, top, ti+consumed) {
					return true
				}
			}
			return false
		}
		if pat[pi] != top[ti] {
			return false
		}
		pi++
		ti++
	}
	return pi == len(pat) && ti == len(top)
}
