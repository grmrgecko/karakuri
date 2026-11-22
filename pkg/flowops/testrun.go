package flowops

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/nodes/core"
)

// errTestParamsMissing is returned when a non-manual trigger has no
// test_params config for a test run.
var errTestParamsMissing = &apperrors.AppError{
	Code:       "TEST_PARAMS_MISSING",
	Message:    "trigger node requires test_params config for test runs",
	StatusCode: http.StatusBadRequest,
}

// TestOptions selects where a test run starts and what seeds it. An empty
// TriggerNodeID resolves the graph's single start candidate; StartNodeID runs
// only the subgraph reachable from that node, seeded from the last persisted
// run. Payload and Topic override the seed message.
type TestOptions struct {
	// RunAs is the user the test run acts as.
	RunAs         uuid.UUID
	TriggerNodeID string
	StartNodeID   string
	Payload       json.RawMessage
	Topic         string
}

// startCandidates returns the nodes a run may start from. Triggers win when the
// graph has any; a graph with none falls back to its pure source nodes (a
// value, a file read, an HA state lookup, output-only nodes with nothing to
// wait for), so a flow built to be driven by hand can still be test-run from
// the value it holds.
func startCandidates(g *flow.Graph) (nodes []*flow.Node, fromValues bool) {
	var triggers, values []*flow.Node
	for i := range g.Nodes {
		meta, ok := engine.Default().MetaFor(g.Nodes[i].Type)
		if !ok {
			continue
		}
		switch {
		case meta.IsTrigger():
			triggers = append(triggers, &g.Nodes[i])
		// Link nodes are pure sources on paper but are wiring: they are
		// spliced out of the executable graph, so a run cannot start at one.
		case meta.Pure && len(meta.Inputs) == 0 && !flow.IsLinkNode(g.Nodes[i].Type):
			values = append(values, &g.Nodes[i])
		}
	}
	if len(triggers) > 0 {
		return triggers, false
	}
	return values, true
}

// selectTriggerNode picks the node a test run starts from: the requested one
// when given, otherwise the graph's single candidate.
func selectTriggerNode(g *flow.Graph, requested string) (*flow.Node, error) {
	candidates, fromValues := startCandidates(g)
	noun := "trigger"
	if fromValues {
		noun = "value"
	}

	if requested != "" {
		for _, n := range candidates {
			if n.ID == requested {
				return n, nil
			}
		}
		return nil, apperrors.ErrValidation.WithMessage(
			"trigger_node_id does not reference a node this flow can start from")
	}
	switch len(candidates) {
	case 0:
		return nil, apperrors.ErrValidation.WithMessage(
			"flow has nothing to start from: add a trigger, or a value node to drive it by hand")
	case 1:
		return candidates[0], nil
	default:
		return nil, apperrors.ErrValidation.WithMessage(
			"flow has multiple " + noun + " nodes; specify trigger_node_id")
	}
}

// buildInitialMsg constructs the seed message for a test run. The manual
// trigger uses its configured payload. Every other trigger uses its
// test_params config; when that is absent, triggers whose metadata declares
// TestWithoutParams (e.g. schedule) run with an empty payload their Execute
// fills in, and all others are rejected.
func buildInitialMsg(trigger *flow.Node, opts TestOptions) (*engine.Msg, error) {
	cfg := map[string]any{}
	if len(trigger.Config) > 0 {
		if err := json.Unmarshal(trigger.Config, &cfg); err != nil {
			return nil, apperrors.ErrValidation.WithMessage("trigger node config is not a valid JSON object")
		}
	}
	meta, _ := engine.Default().MetaFor(trigger.Type)

	if len(opts.Payload) > 0 {
		var payload any
		if err := json.Unmarshal(opts.Payload, &payload); err != nil {
			return nil, apperrors.ErrValidation.WithMessage("test payload is not valid JSON")
		}
		return &engine.Msg{
			ID:      uuid.New().String(),
			Payload: payload,
			Topic:   opts.Topic,
			Meta:    map[string]any{"source": "test"},
		}, nil
	}

	var payload any
	switch {
	case trigger.Type == flow.NodeTypeManual:
		// The manual trigger owns how its configured payload is read, including
		// the output type setting, so ask it rather than reading the raw value.
		p, err := core.ManualTriggerPayload(trigger.Config)
		if err != nil {
			return nil, apperrors.ErrValidation.WithMessage(err.Error())
		}
		payload = p
	case cfg["test_params"] != nil:
		p, err := testParamsPayload(meta, trigger, cfg["test_params"])
		if err != nil {
			return nil, apperrors.ErrValidation.WithMessage(err.Error())
		}
		payload = p
	case meta.TestWithoutParams:
		payload = nil
	default:
		return nil, errTestParamsMissing
	}
	return &engine.Msg{
		ID:      uuid.New().String(),
		Payload: payload,
		Topic:   opts.Topic,
		Meta:    map[string]any{"source": "test"},
	}, nil
}

// testParamsPayload reads a trigger's configured test payload as the type its
// output port declares, so a test run seeds what a real event would deliver.
// A binary trigger is the case that needs converting: the config can only hold
// base64 text, and handing that straight to the run would give every node
// downstream a string where the port promised bytes. Every other port type
// already holds the value it promised.
func testParamsPayload(meta flow.NodeMeta, trigger *flow.Node, value any) (any, error) {
	if declaredOutputType(meta, trigger) != flow.PortBinary {
		return value, nil
	}
	text, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("test payload for a binary trigger must be base64 text")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil {
		return nil, fmt.Errorf("test payload is not valid base64: %w", err)
	}
	return decoded, nil
}

// declaredOutputType returns the payload type of the port a trigger starts a
// run on: its first output that is not a failure branch.
func declaredOutputType(meta flow.NodeMeta, n *flow.Node) string {
	for _, p := range flow.NodePorts(meta, n, nil).Outputs {
		if p.Kind == flow.PortKindError {
			continue
		}
		return p.Type
	}
	return flow.PortAny
}

// firstTriggerNode returns any node the graph can start from, or nil. A partial
// run only needs one to anchor its record, so the choice does not matter.
func firstTriggerNode(g *flow.Graph) *flow.Node {
	candidates, _ := startCandidates(g)
	if len(candidates) == 0 {
		return nil
	}
	return candidates[0]
}

// lastRunOutputs loads the last persisted run's node outputs from its
// node_state events: node ID -> output port -> message. Later events overwrite
// earlier ones so retried nodes report their final outputs.
func lastRunOutputs(flowID uuid.UUID) (map[string]map[string]*engine.Msg, error) {
	run, err := models.FindLastRunByFlow(flowID)
	if err != nil {
		return nil, apperrors.ErrValidation.WithMessage("run-from-node needs a previous run; run the flow once first")
	}
	events, err := models.ListRunEvents(run.ID)
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}

	outputs := make(map[string]map[string]*engine.Msg)
	for _, ev := range events {
		if ev.Event != "node_state" || ev.Payload == "" {
			continue
		}
		var body struct {
			Outputs map[string]struct {
				Payload any            `json:"payload"`
				Topic   string         `json:"topic"`
				Meta    map[string]any `json:"meta"`
			} `json:"outputs"`
		}
		if json.Unmarshal([]byte(ev.Payload), &body) != nil || len(body.Outputs) == 0 {
			continue
		}
		// A node below a fan-out reports once per part, each time with only
		// the ports that part emitted on, so the ports accumulate.
		ports := outputs[ev.NodeID]
		if ports == nil {
			ports = make(map[string]*engine.Msg, len(body.Outputs))
			outputs[ev.NodeID] = ports
		}
		for port, o := range body.Outputs {
			ports[port] = &engine.Msg{
				ID:      uuid.New().String(),
				Payload: o.Payload,
				Topic:   o.Topic,
				Meta:    o.Meta,
			}
		}
	}
	return outputs, nil
}

// buildSeedInputs assembles the start node's input messages for a partial
// run from the last run's stored outputs of its upstream nodes. Upstream
// ports without stored data produce a MISSING_UPSTREAM_DATA error listing
// what is absent.
//
// The edges are read from the graph the run executes, cut down to the start
// node and flattened. Wiring the engine splices out (a reroute, a set/get
// pair, a bypassed node, a subflow boundary) never runs and stores no output,
// so the drawn edge into the start node may name a node the last run has
// nothing for; the flattened edge names the node that produced the message.
func buildSeedInputs(g *flow.Graph, startNodeID string, outputs map[string]map[string]*engine.Msg) (map[string]*engine.Msg, error) {
	g, err := engine.FlattenGraph(flow.ExecutionGraph(g, startNodeID), engine.ResolveSharedSubflowFromDB, engine.Default())
	if err != nil {
		return nil, apperrors.ErrValidation.WithMessage("graph flattening failed: " + err.Error())
	}
	seed := make(map[string]*engine.Msg)
	var missing []string
	for i := range g.Edges {
		ed := &g.Edges[i]
		if ed.Target != startNodeID || ed.IsValue() {
			continue
		}
		msg := outputs[ed.Source][ed.SourceHandle]
		if msg == nil {
			missing = append(missing, fmt.Sprintf("%s.%s", ed.Source, ed.SourceHandle))
			continue
		}
		seed[ed.TargetHandle] = msg
	}
	if len(missing) > 0 {
		return nil, &apperrors.AppError{
			Code:       "MISSING_UPSTREAM_DATA",
			Message:    "the last run has no stored output for upstream nodes: " + strings.Join(missing, ", "),
			StatusCode: http.StatusBadRequest,
		}
	}
	return seed, nil
}

// TestRun strictly validates the draft branch selected for execution and
// starts a test run. With StartNodeID set it runs only the subgraph reachable
// from that node, seeding its inputs from the last persisted run.
func TestRun(f *models.Flow, opts TestOptions) (uuid.UUID, error) {
	if err := RequireEngine(); err != nil {
		return uuid.Nil, err
	}

	// Load flow draft graph.
	draft, err := LoadDraft(f)
	if err != nil {
		return uuid.Nil, err
	}
	g, err := ParseGraph(draft.Graph)
	if err != nil {
		return uuid.Nil, err
	}

	entryID := opts.StartNodeID
	var selectedTrigger *flow.Node
	if entryID == "" {
		selectedTrigger, err = selectTriggerNode(g, opts.TriggerNodeID)
		if err != nil {
			return uuid.Nil, err
		}
		entryID = selectedTrigger.ID
	} else {
		found := false
		for i := range g.Nodes {
			if g.Nodes[i].ID == entryID {
				found = true
				break
			}
		}
		if !found {
			return uuid.Nil, apperrors.ErrValidation.WithMessage("start_node_id does not reference a node in this flow")
		}
	}

	// Strict checks apply only to the branch this run can reach. Draft nodes
	// elsewhere may still be half-configured while the author tests this path.
	if err := ValidateFull(f, withReferencedSubgraphs(flow.ExecutionGraph(g, entryID))); err != nil {
		return uuid.Nil, err
	}

	runOpts := engine.RunOptions{
		FlowID:        f.ID,
		FlowName:      f.Name,
		FlowSlug:      f.Slug,
		FlowVersionID: draft.ID,
		Graph:         g,
		ErrorFlowID:   f.ErrorFlowID,
		RunAs:         opts.RunAs,
	}

	if opts.StartNodeID != "" {
		// Partial run: seed the start node from the last run's outputs.
		// The trigger only anchors the run record; it does not execute,
		// so any trigger node serves when none is selected.
		trigger, err := selectTriggerNode(g, opts.TriggerNodeID)
		if err != nil {
			if trigger = firstTriggerNode(g); trigger == nil {
				return uuid.Nil, err
			}
		}
		outputs, err := lastRunOutputs(f.ID)
		if err != nil {
			return uuid.Nil, err
		}
		seed, err := buildSeedInputs(g, opts.StartNodeID, outputs)
		if err != nil {
			return uuid.Nil, err
		}
		runOpts.TriggerNodeID = trigger.ID
		runOpts.StartNodeID = opts.StartNodeID
		runOpts.SeedInputs = seed
	} else {
		// Full run: resolve the trigger and build the seed message.
		initial, err := buildInitialMsg(selectedTrigger, opts)
		if err != nil {
			return uuid.Nil, err
		}
		runOpts.TriggerNodeID = selectedTrigger.ID
		runOpts.InitialMsg = initial
	}

	// Start the run.
	handle, err := eng.RunFlow(runOpts)
	if err == engine.ErrRunLimit {
		return uuid.Nil, apperrors.ErrRateLimit.WithMessage("flow has too many concurrent runs")
	}
	if err != nil {
		return uuid.Nil, apperrors.ErrInternal.WithError(err)
	}
	return handle.ID, nil
}

// Rerun re-fires the flow's last persisted run: the stored trigger payload is
// replayed through the stored trigger node against the current deployed graph.
func Rerun(f *models.Flow, runAs uuid.UUID) (uuid.UUID, error) {
	if err := RequireEngine(); err != nil {
		return uuid.Nil, err
	}
	if f.DeployedVersionID == nil {
		return uuid.Nil, apperrors.ErrValidation.WithMessage("flow has no deployed version to re-run against")
	}

	// The last persisted run supplies the trigger node and payload.
	lastRun, err := models.FindLastRunByFlow(f.ID)
	if err != nil {
		return uuid.Nil, apperrors.ErrValidation.WithMessage("flow has no persisted run to re-run")
	}
	// A run that resumed a restored wait started at the waiting node with the
	// wait's own message. It has no trigger event, and replaying it would run
	// that node as if it were the trigger.
	if lastRun.ResumedFromRunID != nil {
		return uuid.Nil, apperrors.ErrValidation.WithMessage("the last run resumed a pending wait and has no trigger event to re-run")
	}
	if lastRun.TriggerPayload == "" {
		return uuid.Nil, apperrors.ErrValidation.WithMessage("last run has no stored trigger payload")
	}
	// Replaying the marker would hand the flow a stand-in object as if it
	// were the event.
	if engine.TruncatedPayload(lastRun.TriggerPayload) {
		return uuid.Nil, apperrors.ErrValidation.WithMessage("last run's trigger payload was too large to store and cannot be replayed")
	}
	var payload any
	if err := json.Unmarshal([]byte(lastRun.TriggerPayload), &payload); err != nil {
		return uuid.Nil, apperrors.ErrValidation.WithMessage("last run's stored trigger payload is not valid JSON")
	}

	// Load the deployed graph and check the trigger node still exists.
	version, err := models.FindFlowVersionByID(*f.DeployedVersionID)
	if err != nil {
		return uuid.Nil, apperrors.ErrInternal.WithError(fmt.Errorf("failed to load deployed version: %w", err))
	}
	g, err := ParseGraph(version.Graph)
	if err != nil {
		return uuid.Nil, err
	}
	found := false
	for i := range g.Nodes {
		if g.Nodes[i].ID == lastRun.TriggerNodeID {
			found = true
			break
		}
	}
	if !found {
		return uuid.Nil, apperrors.ErrValidation.WithMessage("the last run's trigger node no longer exists in the deployed graph")
	}

	// Start the run with the replayed payload.
	version.ApplySettings(f)
	handle, err := eng.RunFlow(engine.RunOptions{
		FlowID:        f.ID,
		FlowName:      f.Name,
		FlowSlug:      f.Slug,
		FlowVersionID: version.ID,
		TriggerNodeID: lastRun.TriggerNodeID,
		Graph:         g,
		InitialMsg: &engine.Msg{
			ID:      uuid.New().String(),
			Payload: payload,
			Meta:    map[string]any{"source": "rerun"},
		},
		ErrorFlowID:  f.ErrorFlowID,
		DurableWaits: true,
		RunAs:        runAs,
	})
	if err == engine.ErrRunLimit {
		return uuid.Nil, apperrors.ErrRateLimit.WithMessage("flow has too many concurrent runs")
	}
	if err != nil {
		return uuid.Nil, apperrors.ErrInternal.WithError(err)
	}
	return handle.ID, nil
}

// CancelRun cancels an active run by ID.
func CancelRun(id uuid.UUID) error {
	if err := RequireEngine(); err != nil {
		return err
	}
	if !eng.CancelRun(id) {
		return apperrors.ErrNotFound.WithMessage("no active run with this ID")
	}
	return nil
}

// ActiveRuns returns snapshots of all runs currently executing in memory.
func ActiveRuns() ([]engine.RunSnapshot, error) {
	if err := RequireEngine(); err != nil {
		return nil, err
	}
	return eng.ActiveRuns(), nil
}

// NodeState is the latest observed state of one node within a run.
type NodeState struct {
	State   string `json:"state" description:"Latest node state."`
	Attempt int    `json:"attempt" description:"Latest attempt number."`
	Handled bool   `json:"handled,omitempty" description:"Whether the failure was routed to an error branch."`
	Error   string `json:"error,omitempty" description:"Latest error message."`
}

// RunDetail is the last persisted run of a flow with its events and derived
// per-node latest states.
type RunDetail struct {
	Run        *models.Run          `json:"run" description:"Run row."`
	Events     []*models.RunEvent   `json:"events" description:"Run events in order."`
	NodeStates map[string]NodeState `json:"node_states" description:"Latest state per node."`
}

// LastRunDetail loads a flow's last persisted run and derives the latest state
// per node from its ordered events.
func LastRunDetail(flowID uuid.UUID) (*RunDetail, error) {
	run, err := models.FindLastRunByFlow(flowID)
	if err != nil {
		return nil, apperrors.ErrNotFound.WithMessage("flow has no persisted run")
	}
	return runDetail(run)
}

// RunDetailByID loads one execution and verifies that it belongs to the flow.
func RunDetailByID(flowID, runID uuid.UUID) (*RunDetail, error) {
	run, err := models.FindRunByID(runID)
	if err != nil || run.FlowID != flowID {
		return nil, apperrors.ErrNotFound.WithMessage("run not found on this flow")
	}
	return runDetail(run)
}

func runDetail(run *models.Run) (*RunDetail, error) {
	events, err := models.ListRunEvents(run.ID)
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}

	// Derive the latest state per node from ordered events.
	states := make(map[string]NodeState)
	for _, ev := range events {
		if ev.Event != "node_state" {
			continue
		}
		ns := NodeState{State: ev.State, Attempt: ev.Attempt}
		if ev.Payload != "" {
			var p struct {
				Error   string `json:"error"`
				Handled bool   `json:"handled"`
			}
			if json.Unmarshal([]byte(ev.Payload), &p) == nil {
				ns.Error = p.Error
				ns.Handled = p.Handled
			}
		}
		states[ev.NodeID] = ns
	}
	return &RunDetail{Run: run, Events: events, NodeStates: states}, nil
}
