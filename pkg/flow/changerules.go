package flow

import (
	"encoding/json"
	"strings"
)

// NodeTypeChange is the change node's type name. Its rewrite rules live here
// rather than in the node package so the engine, validation, and the editor
// agree on what a rule may contain.
const NodeTypeChange = "change"

// Change rule operations. Each rule performs one rewrite on the message, and
// rules apply in order so a later rule sees what an earlier one wrote.
const (
	ChangeOpSet    = "set"
	ChangeOpChange = "change"
	ChangeOpDelete = "delete"
	ChangeOpMove   = "move"
	ChangeOpCopy   = "copy"
)

// Change value sources: how a set rule's value, or a change rule's
// replacement, is interpreted.
const (
	ChangeFromString     = "string"
	ChangeFromNumber     = "number"
	ChangeFromBoolean    = "boolean"
	ChangeFromJSON       = "json"
	ChangeFromExpression = "expression"
	ChangeFromProperty   = "property"
)

// Change search modes for the search-and-replace operation.
const (
	ChangeSearchString = "string"
	ChangeSearchRegex  = "regex"
)

// ChangeRule is one rewrite applied to a message by the change node.
type ChangeRule struct {
	// Op is set, change, delete, move, or copy. An empty op means set, which
	// is how rules written before the op field existed are read.
	Op string `json:"op"`
	// Target is the dot path being written, removed, or searched. For move and
	// copy it is the destination; Value holds the source path.
	Target string `json:"target"`
	// From names how Value is interpreted by a set, and how Replace is
	// interpreted by a change. Ignored by delete, move, and copy.
	From string `json:"from,omitempty"`
	// Value is the literal, expression, or source path, depending on Op/From.
	Value any `json:"value,omitempty"`

	// Search is what a change rule looks for in the target property.
	Search string `json:"search,omitempty"`
	// SearchType selects substring or regular-expression matching.
	SearchType string `json:"search_type,omitempty"`
	// Replace is what a change rule writes in place of each match.
	Replace string `json:"replace,omitempty"`
	// ReplaceAll rewrites every match rather than only the first.
	ReplaceAll bool `json:"replace_all,omitempty"`
}

// ChangeConfig is a change node's decoded configuration.
type ChangeConfig struct {
	Rules []ChangeRule `json:"rules"`
}

// DecodeChangeConfig decodes a change node's raw config. An unreadable config
// yields an empty one, which surfaces as a node with no rules.
func DecodeChangeConfig(raw json.RawMessage) ChangeConfig {
	var cfg ChangeConfig
	if len(raw) == 0 {
		return cfg
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return ChangeConfig{}
	}
	return cfg
}

// ChangePorts derives a change node's output port from its rules. A node whose
// last word on the payload is "set it to a string" says so, which is the whole
// point of the node in a graph: the author picked the type, and the canvas can
// show it rather than making the next node guess.
//
// Only the payload root is tracked. A rule writing into the payload leaves it
// an object; one writing the topic or metadata does not touch it at all, so an
// unknown payload stays unknown.
func ChangePorts(raw json.RawMessage) []PortSpec {
	cfg := DecodeChangeConfig(raw)
	// The payload arrives undeclared and each rule may or may not have a say,
	// so the type is whatever the last rule to touch the payload left behind.
	payload := PortAny
	touched := false
	for _, r := range cfg.Rules {
		root, nested := changeTargetScope(r.Target)
		if !root && !nested {
			continue
		}
		touched = true
		if nested {
			// Writing a property into the payload makes it an object whatever
			// the value being written is.
			payload = PortObject
			continue
		}
		payload = changeRulePortType(r)
	}
	// A node whose rules never touch the payload passes it through, so an
	// undeclared result carries the input's type rather than losing it. Once a
	// rule has replaced the payload, what arrived says nothing about what
	// leaves.
	out := PortSpec{ID: "out", Label: "Out", Type: payload}
	if !touched {
		out.TypeFrom = "in"
	}
	return []PortSpec{out}
}

// changeRulePortType maps a rule that rewrites the payload root to the type it
// leaves behind. Expressions, property reads, and search-and-replace all
// produce a value the config cannot name, so they undeclare the payload.
func changeRulePortType(r ChangeRule) string {
	if ChangeRuleOp(r) != ChangeOpSet {
		return PortAny
	}
	switch ChangeRuleFrom(r) {
	case ChangeFromString:
		return PortString
	case ChangeFromNumber:
		return PortNumber
	case ChangeFromBoolean:
		return PortBool
	case ChangeFromJSON:
		return PortObject
	default:
		return PortAny
	}
}

// changeTargetScope classifies a rule's target path: whether it writes the
// payload itself, and whether it writes a property inside it. A path rooted at
// the topic or metadata is neither.
func changeTargetScope(target string) (root, nested bool) {
	path := strings.TrimSpace(target)
	if path == "" || path == "payload" {
		return true, false
	}
	head, _, _ := strings.Cut(path, ".")
	switch head {
	case "topic", "meta":
		return false, false
	}
	return false, true
}

// ChangeRuleOp returns a rule's operation, defaulting to set for rules saved
// without one.
func ChangeRuleOp(r ChangeRule) string {
	if op := strings.TrimSpace(r.Op); op != "" {
		return op
	}
	return ChangeOpSet
}

// ChangeRuleFrom returns a rule's value source, defaulting to string.
func ChangeRuleFrom(r ChangeRule) string {
	if from := strings.TrimSpace(r.From); from != "" {
		return from
	}
	return ChangeFromString
}

// ChangeRuleSearchType returns a change rule's search mode, defaulting to a
// plain substring search.
func ChangeRuleSearchType(r ChangeRule) string {
	if t := strings.TrimSpace(r.SearchType); t != "" {
		return t
	}
	return ChangeSearchString
}

// ChangeOpNeedsSourcePath reports whether an operation reads its source
// property path out of Value, so validation and the editor can require one.
func ChangeOpNeedsSourcePath(op string) bool {
	return op == ChangeOpMove || op == ChangeOpCopy
}

// changeOps is the set of operations a rule may use.
var changeOps = map[string]bool{
	ChangeOpSet: true, ChangeOpChange: true, ChangeOpDelete: true,
	ChangeOpMove: true, ChangeOpCopy: true,
}

// changeValueSources is the set of value interpretations a rule may use.
var changeValueSources = map[string]bool{
	ChangeFromString: true, ChangeFromNumber: true, ChangeFromBoolean: true,
	ChangeFromJSON: true, ChangeFromExpression: true, ChangeFromProperty: true,
}

// changeSearchTypes is the set of search modes a change rule may use.
var changeSearchTypes = map[string]bool{
	ChangeSearchString: true, ChangeSearchRegex: true,
}
