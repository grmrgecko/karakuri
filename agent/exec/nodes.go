package exec

import (
	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// folder is the palette folder this group's nodes list under, nested under the
// agent's own palette group by the host.
const folder = "Exec"

// Nodes returns this group's node types in palette order. A script is text the
// author writes and a shebang resolves; a command is a program that already
// exists. Neither goes through a shell, which is why they are separate types
// rather than one with a mode.
func Nodes(opts agentnode.Options) []nodeapi.NodeType {
	return []nodeapi.NodeType{
		&scriptNode{opts: opts},
		&commandNode{opts: opts},
		&automationScriptNode{opts: opts},
	}
}
