package exec

import (
	"fmt"
	"runtime"
	"time"

	"github.com/grmrgecko/karakuri/agent/agentnode"
)

// processTerminationGrace rejects unsupported shutdown before process or file
// side effects. Windows service agents cannot signal an arbitrary console group.
func processTerminationGrace(raw string) (time.Duration, error) {
	grace, err := agentnode.TerminationGrace(raw)
	if err != nil {
		return 0, err
	}
	if runtime.GOOS == "windows" && grace > 0 {
		return 0, fmt.Errorf("positive termination grace is unsupported on Windows; use 0s for forced termination")
	}
	return grace, nil
}
