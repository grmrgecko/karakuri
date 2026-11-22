package flow

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// NodeTypeSwitch is the switch node's type name. Its output ports are derived
// per instance from its configured rules rather than from static metadata.
const NodeTypeSwitch = "switch"

// Switch rule operators. Each rule owns one output port, so a message can take
// a different branch per rule.
const (
	SwitchOpEq         = "eq"
	SwitchOpNeq        = "neq"
	SwitchOpLt         = "lt"
	SwitchOpLte        = "lte"
	SwitchOpGt         = "gt"
	SwitchOpGte        = "gte"
	SwitchOpContains   = "contains"
	SwitchOpRegex      = "regex"
	SwitchOpExists     = "exists"
	SwitchOpExpression = "expression"
	SwitchOpOtherwise  = "otherwise"
)

// Switch match modes: route to the first matching rule only, or to every
// matching rule.
const (
	SwitchMatchFirst = "first"
	SwitchMatchAll   = "all"
)

// Legacy switch port IDs. Graphs saved before per-rule ports existed wired
// their edges to these two handles, so a config without rules keeps them.
const (
	SwitchPortTrue  = "true"
	SwitchPortFalse = "false"
)

// SwitchRule is one routing rule of a switch node: a test against the node's
// property (or a standalone expression) plus the output port it feeds.
type SwitchRule struct {
	// ID is the rule's stable identity and its output port ID. The editor
	// assigns one per rule so reordering or deleting rules never re-points
	// the edges of the surviving ones.
	ID       string `json:"id,omitempty"`
	Operator string `json:"operator"`
	Value    string `json:"value,omitempty"`
	// Label overrides the port label shown on the canvas.
	Label string `json:"label,omitempty"`
}

// SwitchConfig is a switch node's decoded configuration.
type SwitchConfig struct {
	// Property is the dot path every value rule tests: payload.*, topic, or
	// meta.*. Expression rules ignore it.
	Property string       `json:"property"`
	Rules    []SwitchRule `json:"rules"`
	// Match selects first-match-wins or send-to-every-match routing.
	Match string `json:"match"`

	// Legacy single-condition fields, kept so graphs saved before rules
	// existed keep routing to their true/false ports. See LegacyRules.
	Mode      string `json:"mode"`
	Operator  string `json:"operator"`
	Value     string `json:"value"`
	Condition string `json:"condition"`
}

// DecodeSwitchConfig decodes a switch node's raw config. An unreadable config
// yields an empty one, which surfaces as a node with no output ports.
func DecodeSwitchConfig(raw json.RawMessage) SwitchConfig {
	var cfg SwitchConfig
	if len(raw) == 0 {
		return cfg
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return SwitchConfig{}
	}
	return cfg
}

// IsLegacy reports whether the config predates per-rule ports: no rules, but a
// condition configured the old way.
func (c SwitchConfig) IsLegacy() bool {
	return len(c.Rules) == 0 &&
		(c.Condition != "" || c.Operator != "" || c.Mode != "" || c.Value != "")
}

// EffectiveRules returns the rules to evaluate, with IDs filled in. A legacy
// config is expressed as its equivalent pair of rules on the old true/false
// ports, so callers only ever deal with rules.
func (c SwitchConfig) EffectiveRules() []SwitchRule {
	if c.IsLegacy() {
		cond := SwitchRule{ID: SwitchPortTrue, Label: "True", Operator: c.Operator, Value: c.Value}
		if c.Mode == "expression" {
			cond.Operator = SwitchOpExpression
			cond.Value = c.Condition
		} else if cond.Operator == "" {
			cond.Operator = SwitchOpEq
		}
		return []SwitchRule{
			cond,
			{ID: SwitchPortFalse, Label: "False", Operator: SwitchOpOtherwise},
		}
	}
	rules := make([]SwitchRule, len(c.Rules))
	for i, r := range c.Rules {
		r.ID = SwitchRulePortID(r, i)
		rules[i] = r
	}
	return rules
}

// SwitchRulePortID returns a rule's output port ID, falling back to its
// position for rules saved without one.
func SwitchRulePortID(r SwitchRule, index int) string {
	if r.ID != "" {
		return r.ID
	}
	return fmt.Sprintf("r%d", index+1)
}

// SwitchPorts derives a switch node's output ports from its config: one port
// per rule, or the legacy true/false pair.
func SwitchPorts(raw json.RawMessage) []PortSpec {
	cfg := DecodeSwitchConfig(raw)
	rules := cfg.EffectiveRules()
	ports := make([]PortSpec, 0, len(rules))
	for _, r := range rules {
		ports = append(ports, PortSpec{
			ID:    r.ID,
			Label: SwitchRuleLabel(r),
			// Routing does not change the message, so a branch carries
			// whatever arrived rather than declaring anything of its own.
			Type:     PortAny,
			TypeFrom: "in",
		})
	}
	return ports
}

// SwitchRuleLabel renders the short port label a rule shows on the canvas.
func SwitchRuleLabel(r SwitchRule) string {
	if r.Label != "" {
		return r.Label
	}
	value := strings.TrimSpace(r.Value)
	switch r.Operator {
	case SwitchOpOtherwise:
		return "otherwise"
	case SwitchOpExists:
		return "exists"
	case SwitchOpExpression:
		if value == "" {
			return "expression"
		}
		return truncateLabel(value)
	case SwitchOpContains:
		return "contains " + truncateLabel(value)
	case SwitchOpRegex:
		return "~ " + truncateLabel(value)
	}
	symbols := map[string]string{
		SwitchOpEq:  "=",
		SwitchOpNeq: "≠",
		SwitchOpLt:  "<",
		SwitchOpLte: "≤",
		SwitchOpGt:  ">",
		SwitchOpGte: "≥",
	}
	if sym, ok := symbols[r.Operator]; ok {
		return strings.TrimSpace(sym + " " + truncateLabel(value))
	}
	return r.Operator
}

// truncateLabel keeps port labels short enough not to stretch a node.
func truncateLabel(s string) string {
	const max = 16
	if len([]rune(s)) <= max {
		return s
	}
	return string([]rune(s)[:max-1]) + "…"
}

// SwitchOperatorNeedsValue reports whether an operator compares against a
// configured value, so validation and the editor can require one.
func SwitchOperatorNeedsValue(op string) bool {
	switch op {
	case SwitchOpExists, SwitchOpOtherwise:
		return false
	}
	return true
}

// switchOperators is the set of operators a rule may use.
var switchOperators = map[string]bool{
	SwitchOpEq: true, SwitchOpNeq: true, SwitchOpLt: true, SwitchOpLte: true,
	SwitchOpGt: true, SwitchOpGte: true, SwitchOpContains: true, SwitchOpRegex: true,
	SwitchOpExists: true, SwitchOpExpression: true, SwitchOpOtherwise: true,
}

// SwitchRulesSchema describes the serialized rule list and its port identities.
func SwitchRulesSchema() map[string]any {
	operators := make([]string, 0, len(switchOperators))
	for op := range switchOperators {
		operators = append(operators, op)
	}
	sort.Strings(operators)
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"operator"},
			"properties": map[string]any{
				"id":       map[string]any{"type": "string", "description": "Stable output port ID, used verbatim as source_handle. If omitted, the port is r1, r2, etc. by rule position."},
				"operator": map[string]any{"type": "string", "enum": operators},
				"value":    map[string]any{"type": "string", "description": "Comparison value or expression text. Required and nonempty except for exists and otherwise. Use strings even for numeric comparisons."},
				"label":    map[string]any{"type": "string", "description": "Optional display label; does not change the port ID."},
			},
		},
		"examples": []any{[]any{map[string]any{"id": "high", "operator": "gt", "value": "30"}, map[string]any{"id": "low", "operator": "lte", "value": "30"}}},
	}
}
