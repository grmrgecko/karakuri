package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadPasswordLineKeepsTheWholeLine locks a piped `credential set` to
// storing the password as written. Reading a single whitespace-delimited word
// refused any passphrase holding a space, so such an account could only be
// enrolled from a terminal.
func TestReadPasswordLineKeepsTheWholeLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "single word", input: "hunter2\n", want: "hunter2"},
		{name: "spaces inside", input: "correct horse battery\n", want: "correct horse battery"},
		{name: "windows line ending", input: "two words\r\n", want: "two words"},
		{name: "no line ending", input: " padded ", want: " padded "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readPasswordLine(strings.NewReader(tt.input))
			require.NoError(t, err, "a piped line is a complete password")
			assert.Equal(t, tt.want, got, "only the line ending is stripped from the password")
		})
	}

	_, err := readPasswordLine(strings.NewReader(""))
	assert.Error(t, err, "closed input holds no password to read")
}
