package ai

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestContextOverflowRecognition pins the provider phrasings compaction acts
// on. The llama.cpp row is the error llama-swap returned on 2026-11-08 for
// the editor assistant on gemma-4-26B; the others are the OpenAI and
// Anthropic overflow texts and the throttling and output-limit errors that
// share their words but that compaction cannot fix.
func TestContextOverflowRecognition(t *testing.T) {
	cases := []struct {
		name     string
		err      string
		overflow bool
		limit    int
		request  int
	}{
		{
			name:     "llama.cpp",
			err:      "400 Bad Request: request (33147 tokens) exceeds the available context size (32768 tokens), try increasing it",
			overflow: true, limit: 32768, request: 33147,
		},
		{
			name:     "OpenAI",
			err:      "400 Bad Request: This model's maximum context length is 128000 tokens. However, your messages resulted in 131022 tokens. Please reduce the length of the messages.",
			overflow: true, limit: 128000,
		},
		{
			name:     "Anthropic",
			err:      "400 Bad Request: prompt is too long: 210112 tokens > 200000 maximum",
			overflow: true, limit: 200000,
		},
		{
			name: "rate limit with token wording",
			err:  "429 Too Many Requests: rate limit reached for tokens per minute",
		},
		{
			name: "Bedrock throttle",
			err:  "Too many tokens, please wait before trying again.",
		},
		{
			name: "output limit above the model's",
			err:  "400 Bad Request: max_tokens must be less than or equal to 8192, too many tokens requested",
		},
	}
	for _, tc := range cases {
		err := errors.New(tc.err)
		assert.Equal(t, tc.overflow, IsContextOverflow(err), "overflow recognition for %s", tc.name)
		assert.Equal(t, tc.limit, overflowLimit(err), "window named by %s", tc.name)
		assert.Equal(t, tc.request, overflowRequestTokens(err), "rejected size named by %s", tc.name)
	}
}
