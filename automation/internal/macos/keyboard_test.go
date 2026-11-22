//go:build darwin

package macos

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestUSKeyForPrintableASCII locks that typed text reaches applications that
// read key codes rather than the Unicode string, such as the iOS Simulator:
// every printable ASCII character has a US key position, a shifted character
// shares its key with the unshifted one and adds shift, and no two unshifted
// characters share a key. Before key positions were sent, every character
// arrived there as the "a" key.
func TestUSKeyForPrintableASCII(t *testing.T) {
	unshifted := map[int]rune{}
	for r := rune(' '); r <= '~'; r++ {
		code, shift, ok := usKeyFor(r)
		if !assert.Truef(t, ok, "%q has a US key", r) {
			continue
		}
		if shift {
			continue
		}
		if other, taken := unshifted[code]; taken {
			assert.Failf(t, "two characters share a key", "%q and %q both use key %d", other, r, code)
		}
		unshifted[code] = r
	}

	for _, tc := range []struct {
		r     rune
		base  rune
		shift bool
	}{
		{r: 'a', base: 'a'},
		{r: 'A', base: 'a', shift: true},
		{r: '1', base: '1'},
		{r: '!', base: '1', shift: true},
		{r: '"', base: '\'', shift: true},
		{r: '?', base: '/', shift: true},
	} {
		code, shift, _ := usKeyFor(tc.r)
		base, _, _ := usKeyFor(tc.base)
		assert.Equalf(t, base, code, "%q is on the key of %q", tc.r, tc.base)
		assert.Equalf(t, tc.shift, shift, "%q takes shift: %v", tc.r, tc.shift)
	}
}
