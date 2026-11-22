// Package flow defines the graph document types that describe an automation
// flow, the node metadata vocabulary used to describe node types, and the
// validation rules that keep stored graphs consistent with the node registry.
package flow

import (
	"encoding/json"

	"github.com/google/uuid"
)

// Port type constants describing a port's payload type. Every port carries a
// message whatever its type: the topic and metadata ride along regardless, and
// the type describes only what sits in the payload. That is why there is no
// "message" type — it would name the envelope every port already has, not a
// payload, and it read as a peer of string and float when it never was one.
const (
	// PortAny is a payload whose type is not declared. It is what a node
	// advertises when its output depends on data it has not seen yet — a
	// webhook body, an MQTT payload, whatever a script chose to return — and
	// what an input declares when it takes whatever arrives.
	PortAny  = "any"
	PortBool = "bool"
	// PortNumber accepts either kind of number, for ports that do not care
	// whether a value is whole.
	PortNumber  = "number"
	PortInteger = "integer"
	PortFloat   = "float"
	PortString  = "string"
	// PortObject is a decoded document: an object, array, or the scalar a
	// JSON or YAML payload parsed down to.
	PortObject = "object"
	// PortBinary is real bytes in the payload: what a base64 decode, a binary
	// file read, or a binary HTTP response produces. Scripts are the one place
	// bytes cross as base64 text (sandbox.encodeBinary); inside the engine the
	// payload holds the bytes themselves, and the separate name is what lets a
	// port say it carries encoded bytes rather than text meant to be read.
	PortBinary = "binary"
	// PortImage is an ImageValue: image bytes with their type declared, the
	// form image data travels value edges in (a pull carries the payload
	// alone, so the declaration must ride inside the value). Produced by the
	// Image parser node and consumed by the AI agent's Images input; future
	// image tools share the same type.
	PortImage = "image"
)

// ImageValue is the image port type's payload: base64 bytes with the MIME
// type their producer declared, self-describing so the no-sniffing rule
// holds across value edges where message metadata does not travel.
type ImageValue struct {
	MIME string `json:"mime"`
	Data string `json:"data"`
}

// Compatible reports whether a value of type src may be assigned to a port of
// type tgt. The relation is directional, because assignability is: a declared
// type flows into a port that accepts less specific values, but not the
// reverse.
//
//   - Any type connects to an "any" input: a node that declares what it emits
//     can still feed a node that takes whatever arrives. This is what keeps
//     typing a node from breaking the graphs already wired to it.
//   - An "any" source does not satisfy a typed target. An undeclared value is
//     exactly what a declared type exists to reject.
//   - Integers widen to floats and to "number", so a whole number satisfies a
//     port that asks for either.
//   - Binary widens to string, because base64 text is a string; the reverse
//     does not hold, since arbitrary text is not encoded bytes.
//
// This is the rule for value edges, where a node is asked to supply a config
// field of a declared type. Message edges use MessageCompatible. The widening
// pairs are data on the registered types (PortTypeDef.WidensTo), so a plugin
// type states its own assignability.
func Compatible(src, tgt string) bool {
	if src == tgt || tgt == PortAny {
		return true
	}
	def, ok := PortTypeFor(src)
	if !ok {
		return false
	}
	for _, w := range def.WidensTo {
		if w == tgt {
			return true
		}
	}
	return false
}

// MessageCompatible reports whether an output port of type src may be wired to
// an input port of type tgt on the message path.
//
// It is Compatible with one addition: an "any" source satisfies a typed target.
// On the message path "any" means the payload is not knowable until the run
// happens — a webhook body, an MQTT payload, a script's return value — and
// refusing to wire that into a typed input would forbid the graphs those nodes
// exist for. A typed input still rejects a *declared* mismatch, which is where
// the check earns its keep: an object output wired into a port that wants text
// is a mistake the author can see before deploying.
func MessageCompatible(src, tgt string) bool {
	return src == PortAny || Compatible(src, tgt)
}

// EmptyGraphJSON is the serialized form of a new, empty draft graph.
const EmptyGraphJSON = `{"nodes":[],"edges":[],"viewport":{"x":0,"y":0,"zoom":1}}`

// Graph is the persisted document form of a flow: nodes, edges, and the
// editor viewport. Subgraphs hold local subflows referenced by subgraph_id.
type Graph struct {
	Nodes     []Node           `json:"nodes"`
	Edges     []Edge           `json:"edges"`
	Viewport  Viewport         `json:"viewport"`
	Subgraphs map[string]Graph `json:"subgraphs,omitempty"`
}

// Node is a single placed node instance within a graph.
type Node struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Position XY              `json:"position"`
	Config   json.RawMessage `json:"config,omitempty"`
	// Params lists config field IDs exposed as value input ports, so another
	// node can supply the field instead of it being typed in. This is the
	// "convert widget to input" idea: the field keeps its literal in Config as
	// the fallback, and a wired port overrides it for one execution.
	Params []string `json:"params,omitempty"`
	// Bypass disables the node without unwiring it: the engine
	// splices it out when flattening, forwarding each output from the input
	// whose payload matches it (see BypassPairs). An output no input can
	// honestly feed simply goes dark, so downstream branches wait rather than
	// receive something mislabelled.
	Bypass bool `json:"bypass,omitempty"`
	// Collapsed shrinks the node to its title bar on the canvas.
	// Presentation state only: validation and the engine ignore it, but it is
	// part of the document so the choice survives save and reload.
	Collapsed bool `json:"collapsed,omitempty"`
	// Width and Height record a node the author sized by hand. Most nodes size
	// themselves from their ports and are stored without them; a note is sized
	// to the text it holds, and the size is as much part of the placement as
	// the position is.
	Width  int          `json:"width,omitempty"`
	Height int          `json:"height,omitempty"`
	Retry  *RetryPolicy `json:"retry,omitempty"`
	// ExecuteOnce runs the node for the first message that reaches it in a run
	// and drops the rest. A fan-out delivers one message per part, and the work
	// behind a node is often per-run rather than per-part: notifying once about
	// a batch, or calling an API that only needs asking once. Without it the
	// author puts a limit node in front of everything expensive.
	ExecuteOnce bool       `json:"execute_once,omitempty"`
	SubflowID   *uuid.UUID `json:"subflow_id,omitempty"`
	SubgraphID  string     `json:"subgraph_id,omitempty"`
}

// Edge kinds. A message edge is the control flow: it delivers a message and
// decides what runs next. A value edge delivers nothing; it records that a
// node's parameter is supplied by another node's output, pulled when the
// consumer runs.
const (
	EdgeMessage = "message"
	EdgeValue   = "value"
)

// Edge connects a source node output port to a target node input port.
type Edge struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	SourceHandle string `json:"source_handle"`
	Target       string `json:"target"`
	TargetHandle string `json:"target_handle"`
	// Kind is EdgeValue for a parameter binding; empty or EdgeMessage for the
	// ordinary message path, so graphs written before value edges existed read
	// back unchanged.
	Kind string `json:"kind,omitempty"`
}

// IsValue reports whether the edge binds a parameter rather than delivering a
// message.
func (e Edge) IsValue() bool { return e.Kind == EdgeValue }

// XY is a 2D position in editor canvas coordinates.
type XY struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Viewport is the editor camera state saved with the graph.
type Viewport struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Zoom float64 `json:"zoom"`
}

// RetryPolicy controls per-node retry behavior on execution failure.
type RetryPolicy struct {
	Jitter      float64 `json:"jitter,omitempty" description:"Random delay variation as a fraction from zero to one."`
	MaxAttempts int     `json:"max_attempts" description:"Maximum execution attempts including the first."`
	DelayMs     int     `json:"delay_ms" description:"Initial delay between attempts in milliseconds."`
	Backoff     float64 `json:"backoff" description:"Multiplier applied to the delay after each attempt."`
	MaxDelayMs  int     `json:"max_delay_ms" description:"Upper bound on the delay in milliseconds."`
}
