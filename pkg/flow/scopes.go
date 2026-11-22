package flow

// Variable scopes. Authors name these in node config and in scripts, so the
// canonical list lives here alongside the other shared vocabulary: the engine,
// the script sandboxes, validation, and the editor all read from it rather
// than each spelling the names out.
const (
	// ScopeMsg addresses a property of the message being processed. It is the
	// default everywhere: the payload is what most nodes set after processing.
	ScopeMsg = "msg"
	// ScopeRun addresses a run variable, shared by every node in one run and
	// dropped when it ends. The reserved response_* names shape a synchronous
	// webhook reply.
	ScopeRun = "run"
	// ScopeFlow addresses a flow variable, shared by every node and every run
	// of one flow and persisted across restarts.
	ScopeFlow = "flow"
	// ScopeGlobal addresses a variable shared by every flow, persisted across
	// restarts.
	ScopeGlobal = "global"
)

// Scopes lists every scope in the order they are presented to authors:
// narrowest first, so the default sits at the top.
var Scopes = []string{ScopeMsg, ScopeFlow, ScopeGlobal, ScopeRun}

// ValidScope reports whether s names a scope.
func ValidScope(s string) bool {
	switch s {
	case ScopeMsg, ScopeRun, ScopeFlow, ScopeGlobal:
		return true
	}
	return false
}
