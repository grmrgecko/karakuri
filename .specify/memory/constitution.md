# Karakuri constitution

## Core principles

### I. Failures belong in the automation contract

Requirements MUST describe what happens when a device, node, integration,
script, or remote host fails. Preserve bounded retries, failure routing,
cancellation, and inspectable run results. Do not promise exactly-once delivery
or restart recovery the runtime does not provide. Repeating an action with side
effects requires an intentional retry policy.

### II. Drafts, deployment, and execution are separate

Authors MUST be able to save incomplete drafts without changing deployed
behavior. Deployment MUST freeze a numbered version and follow the feature's
validation contract. Flow deployment requires strict executable-graph validation;
dashboard validation is an explicit authoring check, as defined in features
[002](../../specs/002-flow-authoring/spec.md) and
[006](../../specs/006-dashboard-authoring/spec.md).
Restoration changes the draft; enablement controls armed triggers. Browser,
REST, MCP, and editor chat MUST share service-layer rules. Derived editor ports
MUST agree with server validation.

### III. Permissions and trust are explicit

The server MUST authorize every operation. Dashboard scope MUST derive capabilities
from the applicable layout. Ordinary subsystem permissions and explicit object-level
public policy remain independent authorization paths. Anonymous control requires
explicit public control policy; render credentials add only read capabilities scoped
to one job and dashboard. These paths follow
[feature 007](../../specs/007-dashboard-access-rendering/spec.md).
Script nodes MUST retain their interpreter sandbox. Agent nodes run
under the agent's OS account and MUST NOT be described as sandboxed. Enrollment
trust MUST come from the join token. Revocation closes existing connections
and rejects reconnections.

### IV. Existing state is irreplaceable

Changes to stored data, configuration, flow and dashboard JSON, export files,
REST and MCP interfaces, and the plugin protocol MUST keep existing installations
working or ship a migration. Existing databases, enrolled
identities, CA material, and secrets encrypted with `security.encryption_key` MUST
be preserved.
A plan that makes any of them unreadable MUST identify the break, affected state,
and migration or recovery before implementation. Never regenerate keys, clear
the database, or re-enroll hosts as incidental verification.

### V. Use direct code and appropriate evidence

Use explicit Go control flow and simple structures. Return errors callers can
act on; otherwise log failures and continue safely. Log operational details at
debug, lifecycle events at info, and failures at error. Avoid panics, hidden
side effects, speculative abstractions, and comments that narrate code.
Named-element comments start with the name and form a complete sentence.

Verification MUST use the Makefile. Run relevant `make dev` and `make test`
targets before committing. Prefer real dependencies in tests. Test meaningful
contracts and regressions rather than mirroring implementation. Prefer MCP for
functional checks. UI changes require browser checks of live config-driven
canvas derivations, settled screenshots after interactions, and a console check.
Desktop changes need the applicable live host check in `AGENTS.md` and the
feature's plan.

## Specification and design rules

Read the feature's spec and plan, and its user guide in `docs/` or the module
README, before changing a feature. User guides explain how to set up and use the
software; they do not cite specs or describe internals, which belong in plans. Specs MUST state observable behavior with stable requirement IDs,
prioritized stories, acceptance scenarios, edge cases, and measurable outcomes.
Plans MUST explain architecture, state ownership, interfaces, failures, and
validation. Do not invent performance targets, design history, test evidence,
or completed tasks. Update specs, plans, and user guides when behavior changes.
Keep architecture details out of `AGENTS.md`.

The baseline describes the implemented system as requirements and plans. New
requirements belong in scoped changes, not in notes or proposals.
A change to behavior an existing spec states MUST update that spec and its plan
in place with stable IDs. Create a feature only for a capability no spec owns.
Keep prose direct and consistent with the feature guides.

Product features live in root `specs/`; independently importable automation
library contracts live in `automation/specs/`. Both share this constitution,
toolkit, and template overrides. Number features within their chosen folder and
qualify requirement references with that folder when discussing both scopes.

## Development workflow

Build with `make dev` and `TARGET=server`, `frontend`, `agent`, or `auto`
when appropriate. Test with `make test` and `TARGET=server`, `frontend`,
`agent`, or `automation`. Never invoke compilers or test runners directly.
After building, restart the user `karakuri-dev.service`; never start the
development server with `karakuri serve`. Verify the unit is active and
`http://localhost:8080` answers before handoff. Leave it running.

Preserve unrelated work. Commit each coherent verified change using scoped
Conventional Commits. Do not commit build output, databases, generated app
configs, private agent state, or secrets. Do not create branches, push, or create
issues unless asked. No Git extension is installed. Select specs with
`SPECIFY_FEATURE_DIRECTORY` or ignored local feature state. Toolkit commands and extensions
do not authorize external actions.

## Governance

`AGENTS.md` and explicit user instructions govern repository workflow. This
constitution records those rules for planning and review; it grants no broader
authority. Check these principles before implementation and when reviewing the
final change. Amend the constitution when a durable rule changes, explain why,
and update affected overrides and plans. Use major versions for incompatible
principle changes, minor versions for additions, and patch versions for wording.

**Version**: 1.2.0 | **Ratified**: 2026-10-04 | **Last amended**: 2026-11-21
