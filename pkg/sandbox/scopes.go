package sandbox

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/grmrgecko/karakuri/pkg/flow"
)

// This file holds the scope bookkeeping every runtime needs for get/set. The
// runtimes differ in how a script calls them, not in what they mean, so the
// state and the rules live here once.
//
// Message reads are resolved here because the script has to see a value
// immediately. Message writes are not: they are recorded as paths and applied
// by the script node, so dot-path writing has exactly one implementation.

// scopeState tracks the variable scopes across one script execution. The
// shell interpreter runs pipeline stages and background statements on their
// own goroutines, so get and set hold mu.
type scopeState struct {
	mu     sync.Mutex
	msg    map[string]any
	vars   map[string]any
	flow   map[string]any
	global map[string]any
	writes []MsgWrite
}

// newScopeState builds the state from a request, substituting empty maps so a
// script reading an unset variable gets nil rather than failing.
func newScopeState(req Request) *scopeState {
	return &scopeState{
		msg: map[string]any{
			"payload": req.Payload,
			"topic":   req.Topic,
			"meta":    req.Meta,
		},
		vars:   orEmptyMap(req.Vars),
		flow:   orEmptyMap(req.Flow),
		global: orEmptyMap(req.Global),
	}
}

// orEmptyMap substitutes an empty map for a nil scope.
func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// normalize applies the defaults: an empty scope is the message, an empty name
// is the payload.
func normalize(name, scope string) (string, string, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = flow.ScopeMsg
	}
	if !flow.ValidScope(scope) {
		return "", "", fmt.Errorf("unknown scope %q: use msg, flow, global, or run", scope)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "payload"
	}
	return name, scope, nil
}

// get reads a value from a scope.
func (s *scopeState) get(name, scope string) (any, error) {
	name, scope, err := normalize(name, scope)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch scope {
	case flow.ScopeMsg:
		value, _ := lookupMsgPath(s.msg, name)
		return value, nil
	case flow.ScopeRun:
		return s.vars[name], nil
	case flow.ScopeFlow:
		return s.flow[name], nil
	default:
		return s.global[name], nil
	}
}

// set writes a value into a scope. Message writes are queued rather than
// applied, since the message the script sees is a snapshot.
func (s *scopeState) set(name, scope string, value any) error {
	name, scope, err := normalize(name, scope)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch scope {
	case flow.ScopeMsg:
		s.writes = append(s.writes, MsgWrite{Path: name, Value: value})
	case flow.ScopeRun:
		assign(s.vars, name, value)
	case flow.ScopeFlow:
		assign(s.flow, name, value)
	default:
		assign(s.global, name, value)
	}
	return nil
}

// snapshot copies the scopes and queued message writes into a result. The
// copies keep a background shell statement the script never waited for from
// writing into a result the caller is already reading.
func (s *scopeState) snapshot(res *Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res.Vars = maps.Clone(s.vars)
	res.Flow = maps.Clone(s.flow)
	res.Global = maps.Clone(s.global)
	res.MsgWrites = slices.Clone(s.writes)
}

// scopeObjectName maps a scope to the script global that mirrors it. The run
// scope is named vars in scripts, the name it had before scopes existed. The
// message scope has no object: scripts already receive payload, topic, and
// meta directly.
func scopeObjectName(scope string) string {
	switch scope {
	case flow.ScopeRun:
		return "vars"
	case flow.ScopeFlow:
		return "flow"
	case flow.ScopeGlobal:
		return "global"
	default:
		return ""
	}
}

// assign writes a variable, treating nil as a delete so a script can clear one
// the same way it does by assigning null to the scope object.
func assign(m map[string]any, name string, value any) {
	if value == nil {
		delete(m, name)
		return
	}
	m[name] = value
}

// lookupMsgPath resolves a dot path against the message. It mirrors the
// engine's rule: a path starting with topic or meta addresses that field, and
// anything else is read from the payload.
func lookupMsgPath(msg map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any
	switch parts[0] {
	case "topic":
		cur, parts = msg["topic"], parts[1:]
	case "meta":
		cur, parts = msg["meta"], parts[1:]
	case "payload":
		cur, parts = msg["payload"], parts[1:]
	default:
		cur = msg["payload"]
	}
	for _, part := range parts {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = obj[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}
