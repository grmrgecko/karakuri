# Contract: node API for durable waits

These additions to `pkg/nodeapi`, re-exported by `pkg/engine`, are optional
interfaces. `ExecContext` and `NodeType` keep their method sets, so external
plugins built against the current SDK keep compiling and keep their current
in-memory behavior.

## Wait store

```go
// WaitParker is implemented by execution contexts that can store a pending
// wait so it survives a restart or a redeploy.
type WaitParker interface {
	// WaitStore returns this node's pending-wait store.
	WaitStore() WaitStore
	// ExecutionID identifies this node execution: the run ID and the node
	// instance index. Per-execution waits use it as their key.
	ExecutionID() string
}

// Wait is one stored pending wait.
type Wait struct {
	Token    string          // owner token; set by Arm
	Deadline time.Time       // zero for a state-only row
	RunID    string          // arming run; set by the store
	State    json.RawMessage // node-defined
}

// WaitStore is a node's persisted pending waits, keyed by a node-chosen key.
type WaitStore interface {
	// Get returns the stored wait for key.
	Get(key string) (Wait, bool)
	// Update applies fn under the store lock and stores its result. A nil
	// result deletes the row. Arming with an empty Token assigns a new one.
	// A failure to encode or store leaves the wait in memory only and
	// reports durable == false.
	Update(key string, fn func(cur Wait, ok bool) *Wait) (next Wait, durable bool)
	// Claim deletes the row if its token matches and waits for the delete to
	// commit, returning the wait as claimed. It returns false when the wait
	// was reset, restarted by another owner, cancelled, or dropped. A node
	// emits only after a true Claim.
	Claim(key, token string) (Wait, bool, error)
	// Take applies fn like Update; when fn reports take, the result is
	// claimed instead of stored. Fan-in completion uses it to add the last
	// part and claim the group in one step.
	Take(key string, fn func(cur Wait, ok bool) (next *Wait, take bool)) (Wait, bool, error)
}

// WaitsFor returns the context's wait store and execution ID, or a
// memory-only store kept in the node store for contexts without one.
func WaitsFor(ec ExecContext) (WaitStore, string)
```

`Claim` returns an error only when the delete could not commit. The node then
fails the execution with that error and does not emit. At-most-once delivery
takes precedence over delivery.

## Suspension

```go
// ErrSuspended is the cancellation cause for shutdown and redeploy. A wait
// cancelled with it stays stored and is resumed.
var ErrSuspended = errors.New("suspended; pending waits are carried over")

// Suspended reports whether ctx was cancelled because the run is suspended.
func Suspended(ctx context.Context) bool
```

A node that fails a park with `Suspended(ctx) == true` returns the context
error without deleting its wait. Any other cancellation releases the wait as
it does today.

## Resumable nodes

```go
// Resumer is implemented by node types whose stored waits can resume.
type Resumer interface {
	// Resume continues one stored wait in a new run. It parks until the
	// wait's deadline through the same code Execute uses, claims the wait,
	// and returns the outputs to emit. It returns nil outputs when the claim
	// fails.
	Resume(ctx context.Context, ec ExecContext, key string, w Wait) (map[string][]*Msg, error)
}
```

The engine calls `Resume` only for rows with a non-zero deadline whose node
still exists in the deployed graph with the stored `node_type`, and only after
applying the missed-deadline rule. Parameters are not pulled for a resume. The
node's config comes from the current deployment.

## Shared node fields

`engine.MissedDeadlineFields()` returns the two fields every resumable node
adds to its schema (`internal/nodeutil` cannot hold it, because it imports the
engine):

| Field | Type | Default | Meaning |
| --- | --- | --- | --- |
| `missed_deadline` | select `deliver`, `drop` | `deliver` | What happens to a deadline that came due while the server was down. |
| `missed_grace` | duration | `30s` | A deadline missed by less than this counts as on time. |

Graph validation rejects other `missed_deadline` values through the select's
options. An unreadable or negative `missed_grace` falls back to the default
with a warning, since dropping a wait over a typo would lose it.

`engine.IsDurableRun(ec)` reports whether an execution stores its waits, so a
node can note on its run that a wait could not be stored.
