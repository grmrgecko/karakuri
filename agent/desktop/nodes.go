package desktop

import (
	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/automation"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// folder is the palette folder this group's nodes list under, nested under the
// agent's own palette group by the host.
const folder = "Desktop"

// Nodes returns this group's node types in palette order. These drive whatever
// the session has installed, so a host missing the tool fails the node naming
// what would have worked rather than approximating it.
//
// The automation session is the agent's one, built once and shared with the GUI
// and system groups: a construction error rides onto each node and surfaces on
// first use rather than keeping the palette from loading.
func Nodes(opts agentnode.Options, sess *automation.Session, err error) []nodeapi.NodeType {
	return []nodeapi.NodeType{
		&notifyNode{sess: sess, initErr: err},
		&dialogNode{sess: sess, initErr: err},
		&speakNode{sess: sess, initErr: err},
		&idleTimeNode{sess: sess, initErr: err},
		&mediaNode{sess: sess, initErr: err},
		&mediaStatusNode{},
		&clipboardReadNode{opts: opts, sess: sess, initErr: err},
		&clipboardWriteNode{sess: sess, initErr: err},
		&openURLNode{sess: sess, initErr: err},
		&openAppNode{sess: sess, initErr: err},
		&powerNode{sess: sess, initErr: err},
	}
}
