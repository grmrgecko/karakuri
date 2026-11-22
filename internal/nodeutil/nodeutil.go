// Package nodeutil holds the helpers that node implementations on both sides
// of the wire need. The server's built-in node sets and the agent's are written
// against the same contract (pkg/nodeapi) and emit the same messages, so the
// small things every node does on its way out — copying metadata rather than
// writing through to a message the run history already recorded — were being
// written once per node package. They belong here instead, which is also why
// this is internal: it is shared between the two binaries, not part of the
// contract an external plugin is written against.
package nodeutil

// CloneMeta copies a message's metadata so a node can add to it without writing
// through to the message upstream still holds.
func CloneMeta(meta map[string]any) map[string]any {
	out := make(map[string]any, len(meta)+4)
	for k, v := range meta {
		out[k] = v
	}
	return out
}
