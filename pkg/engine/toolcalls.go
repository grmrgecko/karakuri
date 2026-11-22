package engine

import (
	"sort"
	"sync"
	"time"
)

// defaultToolCallTimeout bounds a graph-backed tool call whose node declared
// no timeout of its own.
const defaultToolCallTimeout = 60 * time.Second

// toolCallResult is what a waiting CallTool receives: the observation message,
// or the error that ended the call.
type toolCallResult struct {
	msg *Msg
	err error
}

// toolCall is one pending graph-backed tool call.
type toolCall struct {
	toolNode string
	args     map[string]any
	ch       chan toolCallResult
}

// toolCallRegistry correlates a tool call's injected group frame with the
// goroutine waiting on its result. It lives on the run and dies with it.
type toolCallRegistry struct {
	mu      sync.Mutex
	pending map[string]*toolCall
	// ended holds calls that timed out, were withdrawn, or already resolved.
	// Their branches keep running, and a later failure or argument read in
	// one still belongs to the call rather than to the run.
	ended map[string]*toolCall
	// touched remembers every tool node that served a call, so settle knows an
	// uncompleted tool node was active rather than skipped.
	touched map[string]bool
}

func newToolCallRegistry() *toolCallRegistry {
	return &toolCallRegistry{
		pending: make(map[string]*toolCall),
		ended:   make(map[string]*toolCall),
		touched: make(map[string]bool),
	}
}

// register opens a pending call and returns its one-shot result channel plus a
// cancel that withdraws the call if it is still pending.
func (t *toolCallRegistry) register(callID, toolNode string, args map[string]any) (<-chan toolCallResult, func()) {
	ch := make(chan toolCallResult, 1)
	t.mu.Lock()
	t.pending[callID] = &toolCall{toolNode: toolNode, args: args, ch: ch}
	t.touched[toolNode] = true
	t.mu.Unlock()
	cancel := func() {
		t.mu.Lock()
		t.endLocked(callID)
		t.mu.Unlock()
	}
	return ch, cancel
}

// resolve completes a pending call with its observation. Only the first
// resolution lands; a later return for the same call reports false.
func (t *toolCallRegistry) resolve(callID string, msg *Msg) bool {
	return t.finish(callID, toolCallResult{msg: msg})
}

// fail completes a pending call with an error.
func (t *toolCallRegistry) fail(callID string, err error) bool {
	return t.finish(callID, toolCallResult{err: err})
}

// finish delivers a result to the pending call and withdraws it. The channel
// is buffered, so delivery never blocks even when the waiter has already left
// on a timeout.
func (t *toolCallRegistry) finish(callID string, res toolCallResult) bool {
	t.mu.Lock()
	call, ok := t.pending[callID]
	t.endLocked(callID)
	t.mu.Unlock()
	if !ok {
		return false
	}
	call.ch <- res
	return true
}

// failAll completes every pending call with err.
func (t *toolCallRegistry) failAll(err error) {
	t.mu.Lock()
	calls := make([]*toolCall, 0, len(t.pending))
	for id, call := range t.pending {
		calls = append(calls, call)
		t.endLocked(id)
	}
	t.mu.Unlock()
	for _, call := range calls {
		call.ch <- toolCallResult{err: err}
	}
}

// endLocked moves a pending call to the ended set. The caller holds mu.
func (t *toolCallRegistry) endLocked(callID string) {
	if call, ok := t.pending[callID]; ok {
		delete(t.pending, callID)
		t.ended[callID] = call
	}
}

// lookupLocked returns a call that is pending or has ended. The caller holds
// mu.
func (t *toolCallRegistry) lookupLocked(callID string) (*toolCall, bool) {
	if call, ok := t.pending[callID]; ok {
		return call, true
	}
	call, ok := t.ended[callID]
	return call, ok
}

// matchReturn finds the pending call a returning message belongs to: the
// innermost group frame registered against the given tool node. Frames pushed
// by fan-out inside the branch sit deeper and are skipped over.
func (t *toolCallRegistry) matchReturn(group []GroupFrame, toolNode string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := len(group) - 1; i >= 0; i-- {
		if call, ok := t.pending[group[i].ID]; ok && call.toolNode == toolNode {
			return group[i].ID, true
		}
	}
	return "", false
}

// argsFor returns the arguments of the call a consumer's group stack sits
// inside, for the given tool node. A call that has ended still answers, since
// its branch may outlive it.
func (t *toolCallRegistry) argsFor(group []GroupFrame, toolNode string) (map[string]any, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := len(group) - 1; i >= 0; i-- {
		if call, ok := t.lookupLocked(group[i].ID); ok && call.toolNode == toolNode {
			return call.args, true
		}
	}
	return nil, false
}

// findActive reports the innermost call an instance's inputs run under, if
// any, whether it is still pending or has ended. Ports are read in sorted
// order so the answer is stable.
func (t *toolCallRegistry) findActive(inputs map[string]*Msg) (string, bool) {
	ports := make([]string, 0, len(inputs))
	for port := range inputs {
		ports = append(ports, port)
	}
	sort.Strings(ports)
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, port := range ports {
		m := inputs[port]
		if m == nil {
			continue
		}
		for i := len(m.Group) - 1; i >= 0; i-- {
			if _, ok := t.lookupLocked(m.Group[i].ID); ok {
				return m.Group[i].ID, true
			}
		}
	}
	return "", false
}

// wasTouched reports whether a tool node served at least one call this run.
func (t *toolCallRegistry) wasTouched(toolNode string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.touched[toolNode]
}
