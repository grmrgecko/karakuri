package main

import (
	"strings"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/grmrgecko/karakuri/internal/service"
	"github.com/stretchr/testify/require"
)

// TestCommandTreeBuilds catches a tag kong rejects, such as an undefined help
// variable, which otherwise panics every command at start, serve included.
func TestCommandTreeBuilds(t *testing.T) {
	_, err := kong.New(&CLI{}, kong.Vars{"serviceActions": strings.Join(service.Actions, ",")})
	require.NoError(t, err)
}
