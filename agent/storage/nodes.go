package storage

import (
	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// folder is the palette folder this group's nodes list under, nested under the
// agent's own palette group by the host.
const folder = "Storage"

// Nodes returns this group's node types in palette order: the reads and writes
// first, then the two that watch. Watch Files is a trigger but lives here
// rather than in a trigger folder, because what it is about is the filesystem.
func Nodes(opts agentnode.Options) []nodeapi.NodeType {
	return []nodeapi.NodeType{
		&fileReadNode{opts: opts},
		&fileStatNode{},
		&fileWriteNode{},
		&fileListNode{},
		&fileCopyNode{},
		&fileMoveNode{},
		&fileWatchNode{},
		&logTailNode{opts: opts},
	}
}
