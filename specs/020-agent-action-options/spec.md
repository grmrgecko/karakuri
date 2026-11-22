# Feature specification: Agent action options and HTTP requests

**Feature**: `020-agent-action-options`
**Created**: 2026-10-06
**Status**: Implemented; verification limits recorded in quickstart.md
**Input**: Extend Run Command, Run Script, Network Probe, Service Control, Write File,
and Image Match, and add HTTP Request on Agent.

## User scenarios and testing

### User story 1: Control command results (Priority: P1)

An author accepts documented exit codes, keeps large logs on the host, and retains
an incoming message when storing the command result.

**Why this priority**: Large output and expected nonzero exits currently fail useful jobs.
**Independent test**: Real fixture programs emit allowed/rejected exits and streams
above 32 MiB; verify output files and messages.

**Acceptance scenarios**:

1. Given accepted exits 0 and 1, command/script exit 1 succeeds with its actual exit
   recorded; exit 2 fails. Launch errors, cancellation and timeout always fail.
2. Given file-only stdout, output above 32 MiB succeeds with complete bytes on disk.
   Discard creates no spool and returns no stream.
3. Given a result property under metadata, decoded stdout is stored there and the
   original payload survives without mutating the input message.

### User story 2: Check health and request host-local APIs (Priority: P1)

An author distinguishes reachability from application health and sends requests from
an agent's network position.

**Why this priority**: Server and agent network access can differ.
**Independent test**: Local HTTP/TLS peers inspect requests and return redirects,
health failures, binary/JSON/text bodies, oversized responses and delayed replies.

**Acceptance scenarios**:

1. Given expected statuses and a body substring, an unexpected response reports
   reachable true and healthy false; configured health failure routes through error.
2. Given HEAD and disabled redirects, the peer receives HEAD and the redirect status
   is observed without following it. Invalid config and cancellation fail.
3. Given HTTP Request on Agent, the peer receives the configured method, interpolated
   URL/headers and selected message body. Output includes decoded response and
   status/header/timing metadata.
4. Given POST failure or timeout, the node does not retry. Oversized response fails
   rather than silently truncating data.

### User story 3: Confirm service actions (Priority: P2)

An author optionally waits for observed state after a service action.

**Why this priority**: An accepted control command does not establish its final state.
**Independent test**: A disposable user service and executable service-manager fixtures
verify running/stopped/enabled/disabled outcomes and confirmation deadlines.

**Acceptance scenarios**:

1. Given confirmation enabled, start/restart waits for running, stop for stopped,
   and enable/disable for observed enabled/disabled configuration.
2. Given unknown/transitional state, no success is claimed. Read errors, timeout and
   cancellation fail without repeating the action.

### User story 4: Guard file replacement (Priority: P2)

An author creates a file without overwriting and checks prior content before replacement.

**Why this priority**: Updates must not discard unexpected existing content.
**Independent test**: Real temporary files verify exclusive creation, hash checks,
permission retention/reset, and unchanged targets after failed guards.

**Acceptance scenarios**:

1. Given create-only mode and an existing file or symlink, the node fails and leaves
   the destination untouched. Concurrent creators cannot overwrite each other.
2. Given expected SHA-256, replacement requires that observed digest; missing,
   non-regular and mismatched targets fail before installation.
3. Given permission preservation enabled (default), replacement retains permissions.
   Disabling it uses normal new-file permissions.

### User story 5: Map image matches to the desktop (Priority: P2)

An author translates image pixels into desktop coordinates when the capture provider
can describe that relationship.

**Why this priority**: Region origins and scaling make image pixels insufficient.
**Independent test**: Capture disposable windows/regions, verify origin and mapped
bounds, and verify binary/unsupported captures do not invent mappings.

**Acceptance scenarios**:

1. Given reliable capture geometry, output reports its desktop bounds, scale and
   mapped match bounds/centers, including negative display origins.
2. Given binary input or a provider without reliable geometry, output reports mapping
   unavailable and does not guess desktop coordinates.

### Edge cases

- Allowed exits are a nonempty list of nonnegative integers; absent means zero only.
  Existing Run Command ignore applies only without an explicit list.
- File-only capture requires a path; discard cannot also mirror a stream.
- HTTP body health matching requires GET and a bounded complete body read.
- Redirects are bounded; custom sensitive headers must not cross origins.
- SHA-256 observes prior content but is not an atomic compare-and-swap against other
  processes. Changes after the final check can race installation.
- Exclusive staged creation requires hard-link support and fails when unavailable.
- Service state does not prove application health or that restart created a new process.
- Capture mapping survives helper transport without changing image pixels.

## Requirements

### Functional requirements

- **FR-001**: Run Command and Run Script MUST expose accepted exit codes, record actual
  exit code, and fail on rejected exits, launch errors, cancellation and timeout.
- **FR-002**: Both nodes MUST expose independent return/file-only/discard stream modes,
  cap each returned stream at 32 MiB, and avoid spools for file-only/discard streams.
- **FR-003**: Both nodes MUST optionally place decoded stdout in a named payload or
  metadata property without mutating input, validating the destination before launch.
- **FR-004**: Network Probe MUST expose expected statuses, GET/HEAD, headers, redirect
  policy and optional body substring; reachable and healthy MUST remain distinct.
- **FR-005**: HTTP Request on Agent MUST expose method, URL, headers, optional message
  body and encoding, timeout, TLS/redirect policies and response format, return status/header/timing
  metadata, and fail on bounded response overflow.
- **FR-006**: HTTP Request on Agent MUST disable automatic retries and fail on configured
  unexpected statuses, invalid config, cancellation and transport errors.
- **FR-007**: Service Control MUST optionally confirm observed target state with bounded
  polling, return its final observation and never replay the action.
- **FR-008**: Write File MUST provide exclusive create-only mode, optional permission
  retention and expected SHA-256 for replacement, retaining staging and rollback.
- **FR-009**: Image Match MUST retain image-pixel results and report optional provider
  mapping and desktop match coordinates without guessing.
- **FR-010**: Agent catalog, server validation and editor derived ports MUST agree.
- **FR-011**: Existing defaults, identities, databases and encrypted secrets MUST remain
  readable; no persistent migration or additional executable dependency is required.

### Key entities

- Process policy: accepted exits, stream modes and result property.
- HTTP policy/result: method, URL, headers, body, statuses, limits and observed health.
- Service confirmation: target, deadline, poll interval and observation.
- File guard: creation mode, previous digest and permission policy.
- Capture mapping: desktop rectangle and desktop units per image pixel.

## Success criteria

- **SC-001**: Accepted nonzero exits and file-only output above 32 MiB succeed; rejected
  exits and oversized returned output fail.
- **SC-002**: Local peers receive configured requests, failed mutations are never replayed,
  and timeout/oversized responses fail.
- **SC-003**: Confirmation succeeds only on observed target states and failed file guards
  preserve prior content.
- **SC-004**: Known mappings agree with captured geometry and unknown mappings produce
  no desktop coordinate claims.

## Assumptions and scope

The selected additions exclude GUI setters, desktop watchers, OCR and fresh file/
clipboard nodes. Confirmation is opt-in and permission preservation defaults to current
behavior. Agent HTTP uses the agent account's network access and is unsandboxed.
Tests use real processes/files and local protocol peers through the Makefile.
