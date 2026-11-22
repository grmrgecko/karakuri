package main

import (
	"strconv"
	"strings"
)

const residentOutputLimit = 4 << 20

// limitedOutput drains verbose children without retaining unbounded output.
type limitedOutput struct {
	buffer    strings.Builder
	truncated bool
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := residentOutputLimit - b.buffer.Len()
	if remaining < len(p) {
		p = p[:remaining]
		b.truncated = true
	}
	_, err := b.buffer.Write(p)
	return n, err
}
func (b *limitedOutput) String() string {
	s := b.buffer.String()
	if b.truncated {
		s += "\n[output truncated at " + strconv.Itoa(residentOutputLimit) + " bytes]\n"
	}
	return s
}

func (b *limitedOutput) Len() int { return b.buffer.Len() }
