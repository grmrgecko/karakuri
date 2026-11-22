# Design plan: Remote agent trust and host actions

**Feature**: `009-remote-agents` | **Date**: 2026-10-04 | **Spec**: [spec.md](spec.md)
**Status**: Implemented baseline

## Summary

Enroll remote hosts with explicit trust, keep their identities valid, and expose
host-specific nodes under the agent account's permissions.

This plan records the design supporting the baseline requirements. It is not
a pending implementation queue. Requirement IDs are local to this feature.

## Technical context and design

`pkg/ca` owns join-token parsing, certificate issuance/verification, and trust
bundles. `pkg/agenthub` owns the TLS listener, enrollment, online connections,
serial revocation, renewal, and root rollover. Agent wire requests and plugin
proxies expose namespaced node types through `pkg/agentwire`, `pkg/agentops`,
and the common node API. `cmd/karakuri-agent` owns enroll/run/service commands.

The agent uses the public plugin interfaces and does not link the server's
models/database. `agent/exec`, `agent/storage`, and `agent/system` perform unsandboxed
host work, using explicit
process cancellation, spooled output, transactional file installation, and
watcher state. Desktop nodes compose the independently specified
[automation module](../../automation/specs/README.md); root spec 010 owns its CLI.

## State and data model

The server database retains agent records, enrollment hashes, revoked serials, and
CA roots and encrypted private keys. Listener certificate/key files live under its data
directory. The host retains its key, certificate, trust
bundle, and resolved address in its identity directory. Default leaf lifetime
is 90 days; reissuance is due with less than a third of the lifetime remaining or
when the issuer changes. Roots live ten years, with automatic overlap starting
one year before expiry. Promotion normally waits for enabled agents with unexpired
certificates to have presented one from the successor (`agents.presented_issuer`);
with less than 30 days left, it promotes anyway so offline records cannot let
the listener expire. Those unrenewed agents must re-enroll. Log-tail checkpoints
use the server's durable node state rather than an agent-local checkpoint database.

## Contracts and failure handling

The join token encodes a URL-safe base64 trust bundle followed by a colon
and secret. Enrollment verifies the fixed server identity `karakuri`, rather
than requiring the configured IP to appear in its certificate. The issued
client certificate must name the agent and match the submitted local key.
Enrollment checks for `agent.json`, not an empty directory. Without it, a retry generates
a fresh key. The hub consumes the token before creating/signing the agent; issuance or
delivery failure can require a new token without a completed local identity.
Agent node types include the stable agent ID. Commands and scripts return
stdout/stderr or fail when collecting more than 32 MiB per stream; disk spools and
optional mirrors are unbounded. Command/script node
execution disables automatic retries. Desktop session-helper forwarding has
the separate retry contract in
[the automation helper spec](../../automation/specs/005-session-access-and-helpers/spec.md).

`agentnode.WithBudget` returns when execution finishes or the resolved timeout plus
explicit confirmation/termination allowances and ten seconds expires. It can abandon
an uninterruptible goroutine; it does not kill arbitrary kernel calls. Triggers keep their subscription context without this timeout.
Unix commands/scripts run in a new process group and cancellation sends that group
SIGKILL by default. Positive `termination_grace` sends SIGTERM first, observes the
group through a bounded grace, and escalates remaining members even if the leader
exits first. Before signaling, cancellation lists processes (gopsutil) and walks parent
links from the group's members; descendants in another group get the same signals,
and the walk repeats before SIGKILL to catch their new children. Windows rejects
positive grace before launch. There the child is created with `CREATE_SUSPENDED`,
assigned to a job object, and resumed through a thread snapshot, since os/exec closes
the thread handle. Cancellation calls `TerminateJobObject`; without a job it runs
`taskkill /T /F` and then kills the parent. The job handle closes after Wait without
kill-on-close, so a process meant to outlive a completed command survives as on Unix.
`processTree` records cleanup failures, which os/exec would otherwise hide behind the
killed parent's exit, and the nodes join them to the cancellation or timeout error.
Staged file replacement attempts rollback, but a rollback failure is reported and
the sequence does not promise crash atomicity.

Tail checkpoints retain file identity and complete-line position in the server's durable
store. Ordinary partial lines replay after restart; oversized lines emit a clipped prefix
and discard the remainder through its newline. A matching identity resumes its offset,
while a replacement at the same path starts at zero. A SHA-256 fingerprint of the first
and last 256 bytes before the offset is checked every poll and on resume; a mismatch
restarts the file at zero, which covers truncation with regrowth and in-place rewrites.
The name is stat'ed before the held file drains. A rotated file is drained until a poll
reads nothing from it, its unterminated last line is reported, and only then is the
replacement opened. One line budget covers the whole poll.

`agent/credstore` seals password entries with their name/user as authenticated data.
The server stores the encrypted per-agent master key and supplies it only to an enabled
agent over its verified TLS identity. The host fetches the key per use and retains no
disk copy. Renewal keeps the private key and live session; new certificates apply on
reconnection. Offered bundles must retain at least one previously trusted root.
Agent renewal checks run immediately and every six hours, retrying failed checks at the
next interval, and run once more right after storing a certificate so the hub sees it
presented. The bundle file is replaced before the certificate file. Every connect and
renewal handshake records the root of its verified chain in `presented_issuer`, an
AutoMigrate column that starts empty and fills on each agent's next handshake.
Promotion counts that column; `cert_issuer` stays the issuance record. Renewal reissues
when the presented root is not the current issuer, so a lost reply or failed write is
issued again. A connect session ends by timer at its certificate's NotAfter. Leaf
lifetimes count from issuance; the one-minute clock-skew backdate no longer shortens
them.

## Constitution check

- Keep the authorization and failure boundaries described above explicit.
- Route authoring through shared operations and preserve draft/deployment separation
  where applicable.
- This baseline adds no schema, wire, identity, or encryption-format migration. Future
  changes must assess the affected persistent state before implementation.
- Use the component checks below and the service handoff checks in `AGENTS.md`.

## Verification

Run `make dev TARGET=agent`, `make dev TARGET=server`, and corresponding `make test`
targets when protocol behavior changes. CA/hub fixtures cover enrollment, revocation,
renewal, and rollover. Serial revocation is checked in CA validation fixtures; a full
operator-revocation exchange with live disconnect and reconnect refusal remains source
only. Agent fixtures cover output spooling, file replacement, credentials, checkpoint
rotation, in-place rewrites, cross-rotation batching, ordinary partial lines, and
oversized-line clipping. Process-group cancellation,
Windows fallback, and wrapper deadlines need live host checks. Live host changes use
canonical executable paths and disposable test files/windows from AGENTS.md.

These are the acceptance checks for changes to this feature. They do not claim
that every live scenario ran while the baseline documents were written.

## Risks and limits

Agent actions can read and change anything their OS account can. A server-side
permission grant is not an OS sandbox. Real identity stores and development CA material
are not disposable test fixtures.
An agent that never presents a successor certificate before the 30-day deadline is
cut off by promotion and must re-enroll.

New behavior needs reviewed requirements and an update to this plan.

## References

- [Server and agent audit](../server-agent-audit.md)

- [System architecture](../architecture.md)
- [README.md](../../README.md)
- [docs/agent-nodes.md](../../docs/agent-nodes.md)
- [pkg/ca/join.go](../../pkg/ca/join.go)
- [pkg/agenthub/agenthub.go](../../pkg/agenthub/agenthub.go)
- [pkg/agenthub/rekey.go](../../pkg/agenthub/rekey.go)
- [cmd/karakuri-agent](../../cmd/karakuri-agent)
- [agent/](../../agent/)

## Install and identity placement

`karakuri-agent service` resolves its scope from the installing account: root or
an administrator gets a system service, anyone else a user service, and
`--scope` overrides. On Windows a user-scope install is a logon scheduled task,
because a Windows service has no per-user form and cannot reach the desktop. The
installed service records the resolved identity directory and server address
(FR-015), so the service account does not have to find the same `agent.yaml`.
`config.DefaultAgentIdentityDir` places the identity by where `agent.yaml` was
found: `/etc/karakuri` maps to `/var/lib/karakuri/agent`, the binary's `etc/` to
its `var/agent`, any other directory to an `agent` directory beside the file, and
no file to the user config directory's `agent`. **Rotate CA** in the agents panel
starts the same overlap and promotion sequence that expiry starts.

On macOS, `service install` and `enroll --service` call
`service.EnsureStableSignature`, which re-signs a binary with a self-signed
host identity so the Accessibility grant, keyed to the designated requirement,
survives updates. A binary that already carries a Developer ID Application
signature is left alone: release builds are signed with the project's
Developer ID and notarized by `docker/macos-sign.sh`, their requirement
(identifier `karakuri-agent` plus team) is already stable, and re-signing would
void the notarization. An agent moving from a self-signed build to a release
build asks for the grant once.

## Container image

`docker/Dockerfile.agent` builds the image from a checkout and
`docker/Dockerfile.agent-release` from the agent's Linux archives, through
`make docker-agent` and `make docker-agent-release`. The release build goes
through `docker/release-image.sh`, which the server image shares. It unpacks one
archive per platform in `DOCKER_PLATFORMS` (default `linux/amd64,linux/arm64`),
fails when one is missing or the versions differ, and builds them as one
multi-platform image with buildx. The checkout build compiles on the build
platform and cross-compiles for the target, so arm64 needs no emulated compile.
Its Go stage is Debian because the Linux binary names glibc's dynamic loader.

The runtime is Debian trixie, so command and script nodes get a normal shell and
`apt-get` for derived images. `/etc/karakuri/agent.yaml` pins the identity
directory to `/var/lib/karakuri/agent` on the image's volume, so `docker exec`
commands and the agent agree on it. `docker/agent-entrypoint.sh` enrolls before
`run` when `agent.json` is absent, and passes `KARAKURI_SERVER` to `run` as the
address override. Other commands run without enrolling. Nodes run as the system
account `karakuri`. Desktop and GUI nodes stay registered and fail with the
session error, since the disable list is the operator's to set.

Verification: build both platforms, run `version` on each, enroll a container
against the development server, run a command node, and restart the container.

## Agent GUI extension

[Feature 013](../013-agent-gui-nodes/plan.md) adds GUI nodes through the existing catalog and execution budget. Native operations delegate through Session.Desktop. State failure evidence uses at most eight seconds of the wrapper grace period. Automation Script reuses command spooling/cancellation with an installed CLI executable.

## Agent action policies

[Feature 020](../020-agent-action-options/plan.md) shares process policies and HTTP
transport, adds bounded service observations, and stages exclusive file creation.
The execution wrapper accepts duration strings and numeric seconds, adding the
confirmation deadline after service control. File-only streams write directly;
discard uses no spool. Agent catalogs expose conditional output types to both the
server resolver and editor. Empty case types retain input propagation.

## Graceful process termination

[Feature 021](../021-graceful-process-termination/plan.md) adds the optional
termination_grace duration to Command and Script, with zero preserving immediate
forced termination. Unix cancellation observes the whole managed group inside the
process callback; the wrapper adds validated grace to its deadline. Parent
cancellation still stops waiting while bounded cleanup continues.

## Browser prompt delegation

[Feature 022](../022-agent-browser-prompts/plan.md) resolves the current Open URL
proxy by agent ID. A server execution-context wrapper supplies only launch config
and ports; attachments and incoming failure messages remain server-side. The human
wait is outside the agent's execution deadline. No protocol or stored-state migration.
