package system

import (
	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/automation"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// folder is the palette folder this group's nodes list under, nested under the
// agent's own palette group by the host.
const folder = "System"

// Nodes returns this group's node types in palette order. Every reading is an
// output-only pure source, which is what keeps it from watching: nothing
// samples in the background, a pull costs one measurement, and the engine
// memoizes it per run. Process Signal is the one that acts.
//
// The sampler is passed in rather than made here because CPU utilization is a
// delta: the previous counters have to outlive one pull. The automation session
// is the agent's one, shared with the desktop and GUI groups; the session nodes
// are the only ones here that use it, and the only ones that reach outside the
// account the agent runs as.
func Nodes(sampler *CPUSampler, sess *automation.Session, err error, creds agentnode.CredentialResolver, opts agentnode.Options) []nodeapi.NodeType {
	return []nodeapi.NodeType{
		&hostInfoNode{sess: sess, initErr: err},
		&systemMetricsNode{sampler: sampler},
		&diskUsageNode{},
		&processListNode{},
		&processWatchNode{},
		&networkProbeNode{opts: opts},
		&httpRequestNode{opts: opts},
		&processSignalNode{},
		&serviceStatusNode{},
		&serviceControlNode{},
		&batteryNode{},
		&sessionListNode{sess: sess, initErr: err},
		&sessionNode{sess: sess, initErr: err, creds: creds},
	}
}

// NewCPUSampler returns the sampler the system metrics node takes its CPU
// deltas against. It belongs to the agent rather than to the node so the
// previous counters survive from one pull to the next.
func NewCPUSampler() *CPUSampler { return &CPUSampler{} }
