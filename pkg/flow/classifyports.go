package flow

import (
	"encoding/json"
	"strings"
)

// NodeTypeAIClassify is the text classifier node type. Its category ports are
// derived here rather than in the node package because the editor needs the
// same answer (lib/utils/payloadPorts.ts mirrors this file), the way the tool
// node's argument ports work.
const NodeTypeAIClassify = "ai-classify"

// classifyPortPrefix namespaces category ports so a category named "error" or
// "other" can never collide with the node's own outputs.
const classifyPortPrefix = "cat:"

// ClassifyPortID returns the output port ID for one category. Categories are
// deduplicated case-insensitively, so the lowercased name is unique and
// stable under a display-case rename.
func ClassifyPortID(category string) string {
	return classifyPortPrefix + strings.ToLower(strings.TrimSpace(category))
}

// SplitTokens splits a tokens-control value into its entries, trimmed, with
// empties dropped and case-insensitive duplicates collapsed onto the first
// spelling. Unlike a keyword list, the entries keep their case: a category is
// a display name.
func SplitTokens(field string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" || seen[strings.ToLower(part)] {
			continue
		}
		seen[strings.ToLower(part)] = true
		out = append(out, part)
	}
	return out
}

// ClassifyPorts derives the classifier's category outputs from its config:
// one port per category, in the order written. Classification routes without
// changing the message, so each port carries whatever arrived. The node's
// static outputs (other, error) are appended after these by NodePorts.
func ClassifyPorts(raw json.RawMessage) []PortSpec {
	var cfg struct {
		Categories string `json:"categories"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &cfg)
	}
	categories := SplitTokens(cfg.Categories)
	ports := make([]PortSpec, 0, len(categories))
	for _, cat := range categories {
		ports = append(ports, PortSpec{
			ID:       ClassifyPortID(cat),
			Label:    cat,
			Type:     PortAny,
			TypeFrom: "in",
		})
	}
	return ports
}
