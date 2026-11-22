package ai

import (
	"regexp"
	"strconv"
	"strings"
)

// overflowPatterns are the phrasings providers use when a request is longer
// than the model's context window, collected from real provider
// responses.
var overflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)prompt is too long`),
	regexp.MustCompile(`(?i)input is too long for (?:the )?(?:requested|selected) model`),
	regexp.MustCompile(`(?i)too large for the model'?s configured input limit`),
	regexp.MustCompile(`(?i)exceeded this model context window limit`),
	regexp.MustCompile(`(?i)exceeds the context window`),
	regexp.MustCompile(`(?i)exceeds (?:the )?(?:model'?s )?maximum context length(?: of [\d,]+ tokens?|\s*\([\d,]+\))`),
	regexp.MustCompile(`(?i)input token count.*exceeds the maximum`),
	regexp.MustCompile(`(?i)number of input tokens exceeds maximum length`),
	regexp.MustCompile(`(?i)tokens in request more than max tokens allowed`),
	regexp.MustCompile(`(?i)maximum prompt length is \d+`),
	regexp.MustCompile(`(?i)reduce the length of the messages`),
	regexp.MustCompile(`(?i)requested input length \d+ exceeds maximum input length`),
	regexp.MustCompile(`(?i)maximum context length is \d+ tokens`),
	regexp.MustCompile(`(?i)exceeds (?:the )?maximum allowed input length of [\d,]+ tokens?`),
	regexp.MustCompile(`(?i)input(?: \(\d+ tokens\))? is longer than the model'?s context length`),
	regexp.MustCompile(`(?i)exceeds the limit of \d+`),
	regexp.MustCompile(`(?i)exceeds the available context size`),
	regexp.MustCompile(`(?i)greater than the context length`),
	regexp.MustCompile("(?i)`inputs` tokens \\+ `max_new_tokens` must be <= \\d+"),
	regexp.MustCompile(`(?i)context window exceeds limit`),
	regexp.MustCompile(`(?i)context window exceeded`),
	regexp.MustCompile(`(?i)exceeded model token limit`),
	regexp.MustCompile(`(?i)context[_ ]length[_ ]exceeded`),
	regexp.MustCompile(`(?i)context length is only \d+ tokens`),
	regexp.MustCompile(`(?i)input length.*exceeds.*context length`),
	regexp.MustCompile(`(?i)prompt (?:too long|exceeds max length|超长)`),
	regexp.MustCompile(`(?i)too large for model with \d+ maximum context length`),
	regexp.MustCompile(`(?i)prompt has [\d,]+ tokens?, but the configured context size is [\d,]+ tokens?`),
	regexp.MustCompile(`(?i)model_context_window_exceeded`),
	regexp.MustCompile(`(?i)range of input length should be`),
	regexp.MustCompile(`(?i)too many tokens`),
	regexp.MustCompile(`(?i)token limit exceeded`),
	regexp.MustCompile(`(?i)request_too_large`),
}

// overflowExclusions are errors that match an overflow phrasing but that
// compaction cannot fix: throttles and an output limit above the model's.
var overflowExclusions = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(throttling error|service unavailable):`),
	regexp.MustCompile(`(?i)rate limit`),
	regexp.MustCompile(`(?i)too many requests`),
	regexp.MustCompile(`(?i)max[_ ]tokens must be less than`),
	regexp.MustCompile(`(?i)too many tokens(?: per day)?, please wait`),
}

// IsContextOverflow reports whether a provider error says the request was
// longer than the model's context window.
func IsContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, pattern := range overflowExclusions {
		if pattern.MatchString(msg) {
			return false
		}
	}
	for _, pattern := range overflowPatterns {
		if pattern.MatchString(msg) {
			return true
		}
	}
	return false
}

// overflowLimitPatterns capture the window size from the overflow errors that
// state it: llama.cpp, OpenAI-style servers, and Anthropic.
var overflowLimitPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)available context size \(([\d,]+) tokens?\)`),
	regexp.MustCompile(`(?i)maximum context length is ([\d,]+) tokens`),
	regexp.MustCompile(`(?i)prompt is too long: [\d,]+ tokens? > ([\d,]+) maximum`),
	regexp.MustCompile(`(?i)configured context size is ([\d,]+) tokens?`),
}

// overflowRequestPattern captures the rejected request's size where the error
// states it, as llama.cpp does.
var overflowRequestPattern = regexp.MustCompile(`(?i)request \(([\d,]+) tokens?\) exceeds`)

// overflowLimit returns the window an overflow error names, or zero.
func overflowLimit(err error) int {
	return firstNumber(err, overflowLimitPatterns...)
}

// overflowRequestTokens returns the rejected request size an overflow error
// names, or zero.
func overflowRequestTokens(err error) int {
	return firstNumber(err, overflowRequestPattern)
}

func firstNumber(err error, patterns ...*regexp.Regexp) int {
	if err == nil {
		return 0
	}
	msg := err.Error()
	for _, pattern := range patterns {
		if m := pattern.FindStringSubmatch(msg); m != nil {
			n, convErr := strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
			if convErr == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}
