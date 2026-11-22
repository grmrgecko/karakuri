package agentnode

import (
	"fmt"
	"strings"
	"time"
)

// TerminationGrace parses the bounded cleanup duration shared by process nodes
// and their execution wrapper. Empty retains immediate forced termination.
func TerminationGrace(raw string) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	grace, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || grace < 0 || grace > time.Minute {
		return 0, fmt.Errorf("termination grace must be a duration from 0s through 1m")
	}
	return grace, nil
}
