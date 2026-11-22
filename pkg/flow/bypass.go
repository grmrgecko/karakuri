package flow

// Bypass turns a node into wiring: the node stays on the canvas, configured
// and connected, but the engine splices it out of the executable graph the
// same way it splices a reroute. What distinguishes bypass from a plain
// passthrough is that a node has real ports with declared types, so each
// output must name which input it forwards — and an output no input can
// honestly feed forwards nothing, rather than relabelling a payload.

// BypassPairs maps each output port of a bypassed node to the input port
// whose message it forwards. The pairing is by declaration, not by sniffing:
// an output that carries TypeFrom already names its input; any other output
// takes the first message input whose declared payload satisfies its own
// (Compatible, the value-edge relation, because downstream wired to the
// output relies on its declared type). Error outputs pair with nothing — a
// bypassed node cannot fail — and parameter inputs are config, not messages,
// so they never forward. Signal inputs also cannot supply payloads.
func BypassPairs(set PortSet) map[string]string {
	pairs := make(map[string]string, len(set.Outputs))
	for _, out := range set.Outputs {
		if out.Kind == PortKindError {
			continue
		}
		if out.TypeFrom != "" {
			if in, ok := set.Input(out.TypeFrom); ok && in.Kind != PortKindParam && in.Kind != PortKindSignal {
				pairs[out.ID] = in.ID
				continue
			}
		}
		for _, in := range set.Inputs {
			if in.Kind == PortKindParam || in.Kind == PortKindSignal {
				continue
			}
			if Compatible(in.Type, out.Type) {
				pairs[out.ID] = in.ID
				break
			}
		}
	}
	return pairs
}

// Bypassable reports whether a placed node's bypass flag means anything: it
// needs a message input to forward from and an output to forward to. Triggers
// and value nodes have no input, annotations have no ports, and the link
// nodes are already wiring — none of them can be bypassed.
func Bypassable(nodeType string, set PortSet) bool {
	if IsLinkNode(nodeType) {
		return false
	}
	if len(set.Outputs) == 0 {
		return false
	}
	for _, in := range set.Inputs {
		if in.Kind != PortKindParam && in.Kind != PortKindSignal {
			return true
		}
	}
	return false
}
