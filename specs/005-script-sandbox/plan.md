# Design plan: Sandboxed logic and server file access

**Feature**: `005-script-sandbox` | **Date**: 2026-10-04 | **Spec**: [spec.md](spec.md)
**Status**: Implemented baseline

## Summary

Allow custom message processing in JavaScript, Python, and Bash with bounded execution
and controlled network access, while keeping host file operations explicit.

This plan records the design supporting the baseline requirements. It is not
a pending implementation queue. Requirement IDs are local to this feature.

## Technical context and design

`pkg/sandbox` embeds goja for JavaScript and mvdan shell for Bash, and runs
CPython 3.12 WebAssembly through wazero. No runtime delegates execution to the
server's installed interpreters. Python may access its interpreter scratch area;
Bash permits `/dev/null`. Neither is general host storage. Runtime bridges encode
binary message content for scripts and mediate every HTTP request through the
same network policy. Capable JavaScript, Python, and Bash flow scripts also
receive generated node-function bindings. Those bindings forward one generic
invocation request to the engine under feature 026; they do not expose drivers or
credentials and are absent from standalone, device, transform, and remote-agent scripts.

`pkg/nodes/core` supplies script and file nodes. Dedicated file operations use
configured storage roots and read limits. Synchronous webhook request handling
waits for the run's response rather than letting a script own the listener.

## State and data model

Each script invocation owns its budgets, captured output, staged variable
writes, and a maximum of 100 admitted node calls. Flow/global writes apply after
successful interpretation; interpreter failures discard them. Later
message-write/output-shape validation can fail after those writes have applied.
Pure script pulls inherit the run's memoization and bypass action retries; they
may invoke only pure node functions. An outer script retry is suppressed after
any call begins execution, while each called node retains its own policy. The Python wasm
asset is install data, not user state. `make`, `make release-assets`, and both
server Dockerfiles fetch the pinned, checksum-verified module into `runtimes/`
unless `PYTHON_SCRIPTS=0`; the release image script creates an empty `runtimes/`
for an archive without it so the image's copy step still works. Allowed server files are persistent host
state; script scratch data is not an alternative route to those files.

## Contracts and failure handling

The per-language bridge exposes the documented message, variable, HTTP,
logging, and response functions in `docs/scripts.md`. Each runtime also exposes
feature 026's node functions or command only when the active execution supplies
an invoker. Calls return port-keyed message arrays and throw catchable invocation
errors or return a structured error with a nonzero shell status.
Multiple script outputs require a port-keyed object. The script's failure port is
`err` unless claimed explicitly.
Network policy checks resolved addresses on connections, including redirects,
with supported URL schemes, 20 calls per execution, and 8 MiB per response body
by default. `CheckRedirect` rejects when five previous requests are present, so
the fifth redirect is not followed. File roots and script
network policy are independent server settings.

## Constitution check

- Keep the authorization and failure boundaries described above explicit.
- Route authoring through shared operations and preserve draft/deployment separation
  where applicable.
- This baseline adds no schema, wire, identity, or encryption-format migration. Future
  changes must assess the affected persistent state before implementation.
- Use the component checks below and the service handoff checks in `AGENTS.md`.

## Verification

Run `make dev TARGET=server` and `make test TARGET=server`. Sandbox tests exercise
runtime failures and network policy; script invocation tests cover call results,
validation, purity, budgets, retry containment, and capability absence. Webhook tests
cover computed synchronous responses. Use `make test-integration` when changing
cross-layer sandbox/webhook behavior. Test Python only with the installed wasm asset,
and report missing runtime verification explicitly.

These are the acceptance checks for changes to this feature. They do not claim
that every live scenario ran while the baseline documents were written.

## Risks and limits

Server file nodes are separate from the script sandbox. Remote command and script nodes
in spec 009 intentionally have host account permissions. Python wall time inside the
guest is frozen at 2022-01-01 because the WebAssembly runtime is instantiated
without host clocks.

New behavior needs reviewed requirements and an update to this plan.

## References

- [Server and agent audit](../server-agent-audit.md)

- [System architecture](../architecture.md)
- [docs/scripts.md](../../docs/scripts.md)
- [pkg/sandbox/sandbox.go](../../pkg/sandbox/sandbox.go)
- [pkg/sandbox/netpolicy.go](../../pkg/sandbox/netpolicy.go)
- [pkg/sandbox/scopes.go](../../pkg/sandbox/scopes.go)
- [pkg/http/webhook.go](../../pkg/http/webhook.go)
- [internal/config/server.go](../../internal/config/server.go)
