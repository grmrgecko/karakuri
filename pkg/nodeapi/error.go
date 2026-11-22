package nodeapi

// ExecutionError carries an intentional retry decision and inspectable failure
// details. Plain errors retain the node's configured retry policy.
type ExecutionError struct {
	Message      string         `json:"message"`
	Retryable    bool           `json:"retryable"`
	RetryAfterMS int64          `json:"retry_after_ms,omitempty"`
	Details      map[string]any `json:"details,omitempty"`
	Cause        error          `json:"-"`
}

func (e *ExecutionError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "node execution failed"
}

// Unwrap retains cancellation and underlying error identity.
func (e *ExecutionError) Unwrap() error { return e.Cause }
