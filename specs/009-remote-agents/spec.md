# Feature specification: Remote agent trust and host actions

**Feature**: `009-remote-agents`
**Created**: 2026-10-04
**Status**: Implemented baseline
**Input**: Enroll remote hosts with explicit trust, keep their identities valid, and
expose host-specific nodes under the agent account's permissions. Ship the agent
as a container image for amd64 and arm64 that enrolls from its environment.

## User scenarios and testing

### User story 1: Enroll a trusted host (Priority: P1)

An operator carries a one-use join token to a host and receives a verified persistent
agent identity.

**Why this priority**: No remote host should execute actions before identity and server
trust are established.

**Independent test**: Enroll against a disposable hub and verify wrong-root, wrong-key,
expired, and reused token refusal.

**Acceptance scenarios**:

1. **Given** a join token carrying CA roots and a live secret, **When** a fresh agent
   enrolls, **Then** it verifies the server against those roots and stores a certificate
   matching its generated key.
2. **Given** a token already consumed or expired, **When** another enrollment is
   attempted, **Then** the hub refuses issuance.

### User story 2: Renew or revoke access (Priority: P1)

An operator retains trusted hosts through certificate rotation and removes a compromised
identity immediately.

**Why this priority**: Long-lived installations need renewal without weakening
revocation.

**Independent test**: Use hub certificate fixtures to renew, roll trust, and revoke a
connected agent.

**Acceptance scenarios**:

1. **Given** a connected agent whose serial is revoked, **When** revocation is applied,
   **Then** its live connection closes and later handshakes are refused.
2. **Given** a root rollover in progress, **When** agents renew, **Then** the trust
   bundle overlaps roots and promotion follows agents presenting successor-issued
   certificates, subject to the hard deadline. Issuance alone does not count.
3. **Given** a connected agent, **When** the certificate it handshook with expires,
   **Then** the hub closes the session and the agent must reconnect with a valid one.

### User story 3: Run host actions and observe files (Priority: P2)

An author runs commands, host scripts, file operations, and host watchers through the
selected agent.

**Why this priority**: Remote effects must retain their host identity and cancellation
semantics.

**Independent test**: Invoke a disposable command, cancel its process tree, and test
transactional file replacement.

**Acceptance scenarios**:

1. **Given** two agents advertising the same node type, **When** the host lists their
   catalogs, **Then** each type is namespaced with its agent ID.
2. **Given** a file replacement whose backup can be restored, **When** installation of
   the staged target fails, **Then** the previous target is restored rather than lost.
3. **Given** installation and backup restoration both fail, **When** replacement
   returns, **Then** its error reports both failures without claiming restoration.

### Edge cases

- An existing `agent.json` prevents another enrollment. A directory without that file
  is not protected by this guard; retrying generates and writes a new private key.
- The hub consumes the join secret before creating the record and signing its leaf.
  A later server failure or lost reply can consume it without completing local enrollment.
- An offline agent missing the certificate/trust overlap may need explicit re-enrollment
  after expiry.
- Command/script execution retries are disabled because repeating a program repeats its
  effects.
- Desktop operations require a logged-in session and supported helpers; host/session
  operations can still work without one.
- A containerized agent's nodes act on the container. Desktop operations have no
  session there and fail with that error. A container recreated without its identity
  volume has no identity and needs a new join token.
- Process cancellation depends on the platform's termination mechanism. A Unix
  descendant that left the launched process group and was orphaned before cancellation
  has no parent link back to the command and is not found. A Windows command the agent
  cannot place in a job object falls back to `taskkill /T /F`, which misses orphans.
- Log tailing is polling, not a lossless journal. A writer that keeps appending to a
  rotated file after it has been quiet for one poll, or a restart while a rotated file
  is still being drained, loses those old-file lines.

## Requirements

### Functional requirements

- **FR-001**: The system MUST carry trusted CA roots and a single-use expiring
  enrollment secret in one join token.
- **FR-002**: The system MUST generate the agent key locally and verify an issued
  certificate against trusted roots, agent identity, and that key before storing it.
- **FR-003**: The system MUST use mutually authenticated agent connections and reject
  revoked certificate serials on every handshake.
- **FR-004**: The system MUST close live agent connections immediately on revocation and
  when the certificate a session authenticated with expires, and keep reconnection
  subject to current enabled/trust state.
- **FR-005**: The system MUST persist agent identity and CA material without
  regenerating them on ordinary builds, upgrades, or restarts.
- **FR-006**: The system MUST automatically attempt agent and listener certificate
  renewal before expiry, reusing the agent key and validating returned agent certificates.
  Agent checks MUST run at startup and every six hours, with failed checks retried at
  the next interval. Unreachable servers or failed storage can prevent completion.
- **FR-007**: The system MUST roll CA roots with overlapping trust and promote after
  enabled agents with unexpired certificates have authenticated with a certificate from
  the successor, or when the outgoing root has less than 30 days remaining; discard old
  roots only after expiry. Disabled agents and agents with expired certificates MUST NOT
  block promotion. Issuance MUST NOT count toward promotion, and renewal MUST reissue
  from the certificate the agent presents, so a lost reply is issued again. The agent
  MUST store the bundle before the certificate and present a newly stored certificate
  at once.
- **FR-008**: The system MUST namespace contributed types as agent.<agent ID>.<type> and
  reject invalid disable-list names before advertising the catalog.
- **FR-009**: The system MUST run host nodes with agent process OS permissions and
  describe them explicitly as unsandboxed.
- **FR-010**: The system MUST support host commands/scripts, files/watchers, system
  information, network probes, services, desktop actions, and session operations through
  advertised capabilities.
- **FR-011**: The system MUST disable automatic command/script retries, spool
  returned stdout/stderr to disk, and cap each returned stream at 32 MiB. Cancellation MUST
  request process-tree termination. On Unix it MUST signal the launched process group
  and the descendants, found through parent links, that moved to another group or
  session. On Windows the command MUST start suspended inside a job object that is
  terminated on cancellation, falling back to `taskkill /T /F` and parent killing when
  the job is unavailable. A cleanup failure MUST be reported with the cancellation or
  timeout error, even when killing the parent succeeds.
  Disk spools and optional file mirrors have no size cap; exceeding the return limit
  MUST fail the node when captured output is collected.
- **FR-012**: The system MUST use sibling staging and rename for file replacement or
  overwrite copy/move, attempting to restore the previous target on failed installation
  and reporting both installation and rollback errors if restoration also fails.
- **FR-013**: The system MUST persist log-tail checkpoints through the server's durable
  node store, resume a valid unexpired checkpoint for a matching file identity at its
  saved complete-line boundary, and read a replacement at the same path from its start.
  It MUST recognize truncation and in-place rewrites by fingerprinting the bytes before
  the offset, including regrowth past the old offset before the next poll and rewrites
  while the agent was stopped. On rename-based rotation it MUST drain the old file
  until a poll reads nothing from it before reading the replacement, and
  `max_lines_per_poll` MUST bound the lines one poll returns across both files.
  Oversized lines MUST be clipped to the read limit, discarding their remainder through
  the next newline; ordinary incomplete lines MUST remain available for checkpoint replay.
- **FR-014**: The system MUST expose agent online state, identity metadata, advertised
  node types, and supported capabilities for authorized inspection.
- **FR-015**: The system MUST install agent services using supported user/system scopes
  and retain the resolved identity directory and server address in service
  configuration.
- **FR-016**: Every agent node execution MUST return by its resolved timeout plus a
  ten-second cleanup margin and any supported explicit confirmation or process
  termination allowance, defaulting to the agent's 60-second execution budget. This
  wrapper MUST NOT time-limit trigger subscriptions. Cancellation MUST stop waiting,
  without promising to interrupt an OS call or helper that ignores its context.
- **FR-017**: The agent MUST store named account passwords encrypted with AES-256-GCM,
  binding each ciphertext to its entry name and username. It MUST fetch that agent's
  master key per use over authenticated TLS, keep the key off agent disk, and leave
  credential listing/removal available without fetching it. The server MUST encrypt
  the master key under `security.encryption_key` and refuse disabled or revoked agents.
- **FR-018**: Accepted enrollment and renewal trust bundles MUST share at least one
  root with the previously trusted bundle. Renewed certificates MUST take effect on
  the next connection; renewal MUST NOT replace the private key or restart a live session.

- **FR-019**: The project MUST publish a `karakuri-agent` container image for
  linux/amd64 and linux/arm64 under one tag. On a start with no stored identity it
  MUST enroll from `KARAKURI_SERVER` and `KARAKURI_JOIN_TOKEN`, and refuse to start
  with an error naming both when either is missing. It MUST keep the identity on a
  volume so a recreated container reconnects as the same agent without the token,
  and it MUST run nodes as an unprivileged account by default.

- **FR-020**: The agents settings page MUST link to the published releases and say
  which agent archive fits a host, how to unpack and install it, and that the
  container image is the alternative, before the enrollment form.
- **FR-021**: An agent MUST belong to the user who minted its enrollment token, and
  be listed, changed, and run only by its owner, users it is shared with, and holders
  of `api:agents:manage`, as [feature 047](../047-connection-agent-access/spec.md)
  defines. Enrollment tokens MUST be listed to their minter and to managers.

### Key entities

- Agent: stable ID, enabled/online state, advertised catalog, and certificate serial.
- Enrollment and trust bundle: expiring one-use secret, CA roots, signing/overlap state.
- Identity directory: private key, certificate, server address, and trusted bundle.
- Execution request: node/config/input context, cancellation, outputs, and error.

## Success criteria

- **SC-001**: Every invalid/reused enrollment fixture fails before storing a complete
  enrolled identity; a valid certificate matches the agent key. Failed enrollment MAY
  leave the locally generated private-key file.
- **SC-002**: Revocation disconnects a live agent and all later handshakes using its
  serial fail.
- **SC-003**: Renewal preserves the private key. A rollover does not promote on an issued
  but unpresented successor certificate, reissues to an agent still presenting the old
  root, and promotes once the successor certificate is presented. A session ends when
  its certificate expires and the expired certificate cannot reconnect.
- **SC-004**: Unix command cancellation leaves no live fixture processes in its launched
  process group, or descendants that called setsid, when the OS permits termination.
  Windows cancellation stops a grandchild whose parent already exited. Failed file
  installation preserves original contents when rollback succeeds; failed rollback
  reports both errors.
- **SC-005**: Both architectures of the agent image report the agent version. A
  container enrolls with a fresh token, runs a command node as its unprivileged
  account, and reconnects as the same agent after a restart.

## Assumptions and scope

Agent actions can read and change anything their OS account can. A server-side
permission grant is not an OS sandbox. Real identity stores and development CA material
are not disposable test fixtures.
File replacement has a staging/backup/rename sequence, not a crash-atomic transaction
over multiple filesystem operations. Log-tail checkpoints are written through the
server's durable node store and can replay an unflushed interval after interruption.
A rewrite that reproduces the fingerprinted bytes exactly reads as unchanged.
Renewal replaces the CA bundle and certificate as two files, bundle first. A failure
between them leaves the old certificate with a bundle that still verifies the host, and
the next check reissues. An agent that stays offline past the 30-day promotion deadline
without presenting a successor certificate still needs re-enrollment.

## References

- [Design plan](plan.md)
- [System architecture](../architecture.md)
- [README.md](../../README.md)
- [docs/agent-nodes.md](../../docs/agent-nodes.md)
- [pkg/ca/join.go](../../pkg/ca/join.go)
- [pkg/agenthub/agenthub.go](../../pkg/agenthub/agenthub.go)
- [pkg/agenthub/rekey.go](../../pkg/agenthub/rekey.go)
- [cmd/karakuri-agent](../../cmd/karakuri-agent)
- [agent/](../../agent/)

## Agent GUI extension

[Feature 013](../013-agent-gui-nodes/spec.md) defines execution-triggered GUI observations, controls, waits, menus, dialogs, displays, and Automation Script. Existing pure sources remain available. Ambiguous window action patterns now fail instead of selecting the first match. No identity or persistent-state migration is required.

## Agent action extension

[Feature 020](../020-agent-action-options/spec.md) defines accepted exits, direct
file/discard streams, result properties, host HTTP requests and health checks,
service confirmation, and guarded file creation/replacement. Defaults retain the
contracts above. File-only/discard streams bypass return collection and spooling.
No identity, credential, or database migration is required.

## Browser prompt delegation

[Feature 022](../022-agent-browser-prompts/spec.md) uses the enrolled agent's
existing Open URL node. The server owns question data, answer authority and waiting;
only the launch URL is sent to the agent. Agent identity and credentials are unchanged.
