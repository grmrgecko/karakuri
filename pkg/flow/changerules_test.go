package flow

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChangeRuleDefaults covers reading a rule saved before the op, from, and
// search_type fields existed. Graph documents are stored JSON that outlives the
// build that wrote them, so the absent-field reading is an on-disk format
// invariant: a flow deployed a year ago must keep doing what it did.
func TestChangeRuleDefaults(t *testing.T) {
	stored := ChangeRule{Target: "payload.x", Value: "y"}

	assert.Equal(t, ChangeOpSet, ChangeRuleOp(stored), "a rule with no op is a set, which is all rules could do then")
	assert.Equal(t, ChangeFromString, ChangeRuleFrom(stored), "a rule with no value source held a literal string")
	assert.Equal(t, ChangeSearchString, ChangeRuleSearchType(stored), "a rule with no search type matched literally, never as a regex")
}

// TestDecodeChangeConfig covers reading the rule list off a node's stored
// config. A config the decoder cannot read has to come back as no rules rather
// than a panic: the same decode runs on the canvas for every node on screen,
// so a hand-edited graph would otherwise take the editor down.
func TestDecodeChangeConfig(t *testing.T) {
	cfg := DecodeChangeConfig(json.RawMessage(`{"rules":[
		{"op":"change","target":"payload.name","search":"old","replace":"new","replace_all":true}
	]}`))
	require.Len(t, cfg.Rules, 1, "one rule was stored, so one must decode")

	rule := cfg.Rules[0]
	assert.Equal(t, "old", rule.Search, "the search text must survive the decode")
	assert.Equal(t, "new", rule.Replace, "the replacement must survive the decode")
	assert.True(t, rule.ReplaceAll, "replace_all changes how many matches are rewritten and must survive")

	assert.Empty(t, DecodeChangeConfig(json.RawMessage(`{"rules":"nope"}`)).Rules,
		"a config the decoder cannot read must yield no rules rather than panic on the canvas")
}

// TestValidateChangeRules covers what a change node is allowed to hold, in both
// validation modes. The split is the point: a draft save is lenient so a
// half-typed rule can be stored, and deploy is strict so a broken rule cannot
// reach a running flow. Every row that differs between the two modes is a rule
// an author is mid-way through writing.
func TestValidateChangeRules(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		lenient bool
		wantErr bool
		why     string
	}{
		{name: "valid set", config: `{"rules":[{"op":"set","target":"payload.x","from":"number","value":1}]}`},
		{
			name:   "empty string set is allowed",
			config: `{"rules":[{"op":"set","target":"payload.x","from":"string","value":""}]}`,
			why:    "blanking a field is a real edit, not a missing value",
		},
		{name: "no rules", config: `{}`, wantErr: true, why: "a change node that changes nothing is a deploy-time mistake"},
		{name: "no rules draft", config: `{}`, lenient: true, why: "a node just dropped on the canvas has no rules yet"},
		{name: "unknown op", config: `{"rules":[{"op":"frobnicate","target":"payload.x"}]}`, wantErr: true},
		{name: "no target", config: `{"rules":[{"op":"set","value":"x"}]}`, wantErr: true},
		{name: "no target draft", config: `{"rules":[{"op":"set","value":"x"}]}`, lenient: true},
		{name: "unknown value source", config: `{"rules":[{"op":"set","target":"payload.x","from":"telepathy"}]}`, wantErr: true},
		{
			name:    "empty expression",
			config:  `{"rules":[{"op":"set","target":"payload.x","from":"expression","value":""}]}`,
			wantErr: true,
			why:     "an expression source with nothing to evaluate cannot produce a value",
		},
		{name: "valid delete", config: `{"rules":[{"op":"delete","target":"payload.x"}]}`},
		{name: "move without source", config: `{"rules":[{"op":"move","target":"payload.x"}]}`, wantErr: true},
		{name: "move without source draft", config: `{"rules":[{"op":"move","target":"payload.x"}]}`, lenient: true},
		{name: "valid move", config: `{"rules":[{"op":"move","target":"payload.x","value":"payload.y"}]}`},
		{name: "valid change", config: `{"rules":[{"op":"change","target":"payload.x","search":"a","replace":"b"}]}`},
		{name: "change without search", config: `{"rules":[{"op":"change","target":"payload.x","replace":"b"}]}`, wantErr: true},
		{
			name:    "change unknown search type",
			config:  `{"rules":[{"op":"change","target":"payload.x","search_type":"glob","search":"a"}]}`,
			wantErr: true,
		},
		{
			name:    "change bad regex",
			config:  `{"rules":[{"op":"change","target":"payload.x","search_type":"regex","search":"a("}]}`,
			wantErr: true,
			why:     "an unparseable pattern fails at the first message, so it is caught at deploy instead",
		},
		{
			name:    "change bad regex draft",
			config:  `{"rules":[{"op":"change","target":"payload.x","search_type":"regex","search":"a("}]}`,
			lenient: true,
			why:     "a pattern is unparseable for most of the time it is being typed",
		},
		{
			name:   "change templated regex",
			config: `{"rules":[{"op":"change","target":"payload.x","search_type":"regex","search":"{{ payload.p }}("}]}`,
			why:    "a pattern built at runtime cannot be compiled now, so it is not judged now",
		},
	}

	for _, c := range cases {
		p := &problems{details: map[string]string{}}
		node := &Node{ID: "n1", Type: NodeTypeChange, Config: json.RawMessage(c.config)}

		validateChangeRules(p, "node:n1", node, c.lenient)

		if c.wantErr {
			assert.NotEmptyf(t, p.details, "%s must be reported: %s", c.name, c.why)
		} else {
			assert.Emptyf(t, p.details, "%s must be accepted (%s), got %v", c.name, c.why, p.details)
		}
	}
}
