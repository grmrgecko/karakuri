# Feature specification: Sandboxed logic and server file access

**Feature**: `005-script-sandbox`
**Created**: 2026-10-04
**Status**: Implemented baseline
**Input**: Allow custom message processing in JavaScript, Python, and Bash with bounded
execution and controlled network access, while keeping host file operations explicit.
Add mediated script-hosted node calls under feature 026 without weakening interpreter isolation.
Extend those calls to Python and Bash while preserving each runtime's existing data, error, and
capability boundaries.

## User scenarios and testing

### User story 1: Transform a message safely (Priority: P1)

An author computes a payload or branches to declared outputs without access to the
server operating system.

**Why this priority**: User logic must not turn the flow editor into a host shell.

**Independent test**: Run scripts in each available runtime and attempt prohibited
filesystem and process access.

**Acceptance scenarios**:

1. **Given** a script with several declared outputs, **When** it returns an object keyed
   by output IDs, **Then** only present keys emit branch payloads.
2. **Given** a script attempts host file or process access, **When** the interpreter
   evaluates it, **Then** the unavailable operation fails without exposing the host.
3. **Given** a server flow script invokes an eligible node function, **When** the call
   crosses the interpreter boundary, **Then** feature 026's mediated execution applies
   without granting direct host, credential, or integration-driver access.

### User story 2: Control outbound work (Priority: P1)

An operator sets the network policy and execution budgets for user scripts.

**Why this priority**: A script must not hang a run or bypass a configured network
boundary.

**Independent test**: Run a looping script and exercise public/private HTTP destinations
under each policy.

**Acceptance scenarios**:

1. **Given** network policy none, **When** a script requests an HTTP URL, **Then** the
   request is refused.
2. **Given** network policy bogons, **When** a hostname or redirect resolves to a
   private address, **Then** the resolved connection is refused.

### User story 3: Answer requests and read allowed files (Priority: P2)

An author computes a synchronous webhook response or uses a dedicated file node for
allowed storage.

**Why this priority**: HTTP responses and host files need explicit capabilities.

**Independent test**: Test a script-computed webhook reply and file access outside
configured roots.

**Acceptance scenarios**:

1. **Given** a synchronous webhook run, **When** a script sets the response and
   finishes, **Then** the waiting HTTP request receives the computed status, headers,
   and body.
2. **Given** configured file roots, **When** a server file node requests an escaping
   path, **Then** the access is refused.

### Edge cases

- Python is selectable but fails clearly when its WebAssembly runtime asset is missing.
- Captured-output exhaustion truncates console output without failing an otherwise
  successful script.
- Interpreter failure does not commit staged flow/global variable writes. A later
  message-write or output-shape error can fail the node after those writes were applied.
- A script claiming output ID err suppresses the automatically added error port of that
  ID.

## Requirements

### Functional requirements

- **FR-001**: The system MUST run JavaScript, Python, and Bash in isolated interpreters
  with no host filesystem, subprocess, socket, or host environment capability.
- **FR-002**: The system MUST expose only documented message inputs, output helpers,
  variables, logging, response helpers, mediated HTTP access, and the mediated server
  flow node functions defined by feature 026 in capable script contexts. Device,
  transform, standalone-test, and remote-agent scripts MUST NOT gain node invocation
  implicitly.
- **FR-003**: The system MUST resolve script message ports from configuration,
  defaulting to one untyped input and output when none are declared.
- **FR-004**: The system MUST require a multi-output script result to be keyed by
  declared output ID and leave omitted branches un-emitted.
- **FR-005**: The system MUST provide the script failure output unless that ID is
  explicitly declared by the author and apply message-path engine retries before
  failure routing, except that once any script-hosted call has begun execution an outer
  retry MUST NOT replay it or earlier calls under feature 026. Pure pulls use the
  parameter-failure contract in spec 003 and may invoke only pure node functions.
- **FR-006**: The system MUST honor the configured timeout or node override, defaulting
  to 30 seconds, and terminate execution on timeout or cancellation.
- **FR-007**: The system MUST bound captured output to 256 KiB by default and append a
  truncation marker when the budget is exhausted.
- **FR-008**: The system MUST bound HTTP calls to 20 per execution and each response
  body read to 8 MiB by default, with configured overrides.
- **FR-009**: The system MUST accept HTTP and HTTPS only, reject the fifth redirect hop,
  and enforce full, bogons, or none network policy for every connection.
- **FR-010**: The system MUST under bogons policy reject reserved/private resolved IPs
  and redirected destinations instead of trusting hostname spelling.
- **FR-011**: The system MUST expose Python only through its installed CPython
  WebAssembly asset; explain a missing runtime and preserve its documented frozen clock
  behavior. Builds, release archives, and container images MUST include the asset
  unless built with `PYTHON_SCRIPTS=0`.
- **FR-012**: The system MUST apply staged persistent variable writes only when the
  interpreter succeeds and keep msg/run scope separate from durable flow/global scope.
  The current node applies these writes before message-write and multi-output shape
  validation; a subsequent node failure does not roll them back.
- **FR-013**: The system MUST allow pure script outputs as memoized value producers and
  expose explicit synchronous webhook response helpers.
- **FR-014**: The system MUST constrain dedicated server file nodes to configured
  storage roots, resolving path escape and enforcing read-size limits.

### Key entities

- Script configuration: language, code, declared ports, timeout, and parameter bindings.
- Execution budget: wall time, captured output, HTTP request count, and response-size
  limits.
- Network policy and storage roots: separate grants for mediated HTTP and dedicated
  file-node access.

## Success criteria

- **SC-001**: Filesystem/process attempts fail in every available runtime; normal
  payload transformations produce the declared outputs.
- **SC-002**: Private-address and redirect fixtures fail under bogons, and all outbound
  requests fail under none.
- **SC-003**: An infinite script terminates at its timeout and a successful verbose
  script reports truncated output.
- **SC-004**: An interpreter failure leaves staged persistent variables unchanged and a
  synchronous webhook returns its explicit response.
- **SC-005**: A mediated node call succeeds in each supported runtime without direct
  host capability, an impure call during a pure pull is refused, and a standalone or
  device script has no node invocation global, command, or other capability.

## Assumptions and scope

Server file nodes are separate from the script sandbox. Remote command and script nodes
in spec 009 intentionally have host account permissions. Python wall time inside the
guest is frozen at 2022-01-01.

## References

- [Design plan](plan.md)
- [Script node SDK](../026-script-node-sdk/spec.md)
- [System architecture](../architecture.md)
- [docs/scripts.md](../../docs/scripts.md)
- [pkg/sandbox/sandbox.go](../../pkg/sandbox/sandbox.go)
- [pkg/sandbox/netpolicy.go](../../pkg/sandbox/netpolicy.go)
- [pkg/sandbox/scopes.go](../../pkg/sandbox/scopes.go)
- [pkg/http/webhook.go](../../pkg/http/webhook.go)
- [internal/config/server.go](../../internal/config/server.go)
