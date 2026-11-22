package render

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

// TestDiagnosticText pins the boundary that keeps browser URL credentials and
// render grants out of persisted diagnostics while preserving request paths.
func TestDiagnosticText(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"GET http://user:password@localhost:8080/api?token=secret#fragment failed", "GET http://localhost:8080/api failed"},
		{"render token secret failed", "render token [redacted] failed"},
		{"TypeError: cannot read property width", "TypeError: cannot read property width"},
	} {
		assert.Equal(t, tc.want, diagnosticText(tc.input, "secret"), "diagnostic must retain context without URL credentials")
	}
}
