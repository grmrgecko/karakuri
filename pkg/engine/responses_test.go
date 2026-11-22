package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestResponseFromVarsCanonicalizesHeaderNames covers a script that sets a
// response header in another letter case than the node's default. The webhook
// handler writes the map with Header().Set in map order, so two spellings of
// one header left the reply's content type to chance. The variable has to
// replace the default under the one canonical name.
func TestResponseFromVarsCanonicalizesHeaderNames(t *testing.T) {
	base := &HTTPResponse{Status: 200, Headers: map[string]string{"Content-Type": "application/json"}}
	vars := map[string]any{VarResponseHeaders: map[string]any{
		"content-type": "text/html",
		"x-count":      3,
	}}

	out := ResponseFromVars(vars, base)

	assert.Equal(t, map[string]string{"Content-Type": "text/html", "X-Count": "3"}, out.Headers,
		"a header set by variable must override the default whatever case it was written in")
	assert.Equal(t, "application/json", base.Headers["Content-Type"],
		"the base response belongs to the node's configuration and must not be changed")
}
