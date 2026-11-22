# Specification source audit

**Date**: 2026-10-04
**Runtime baseline**: `5158025`
**Initial specification commit**: `8a04fe3`

This report records the initial targeted audit. The later
[server and agent audit](server-agent-audit.md) covers all requirements in those
features and records their current verification evidence.

The initial specs followed the feature guides and selected implementation paths.
Passing the repository test groups did not establish that every requirement was
accurate. A follow-up source review found the mismatches below and corrected the
specs and plans without changing runtime behavior.

## Corrections and evidence

| Contract | Corrected behavior | Source |
| --- | --- | --- |
| 002 FR-002, 003 FR-001 | Deployment strictly validates trigger-reachable branches and value dependencies. Shared subflows and graphs without triggers validate in full. Test runs validate the selected execution graph; explicit strict validation checks the full document. Unavailable node types can remain in lenient drafts. | [flow operations](../pkg/flowops/flowops.go), [test runs](../pkg/flowops/testrun.go), [validation](../pkg/flow/validate.go) |
| 002 FR-005 and plan | Whole-graph saves accept an optional expected revision; focused patches require it. Revisions hash graph content, excluding metadata. Saves also compare stored graph content inside the transaction. Flow records reference separate draft/deployed version rows. | [draft saving](../pkg/flowops/flowops.go), [patching](../pkg/flowops/patch.go), [flow model](../pkg/models/flow.go) |
| 002 FR-007, FR-013 | Known-node config validation uses metadata. Export blanks declared connection fields, including non-secret instance references; unknown-node fields cannot be classified. | [validation](../pkg/flow/validate.go), [export](../pkg/flowops/export.go) |
| 005 FR-008, FR-009 | The response limit is per body, while the call budget is per execution. The fifth redirect hop is rejected. | [sandbox budgets](../pkg/sandbox/sandbox.go), [HTTP policy](../pkg/sandbox/netpolicy.go) |
| 007 FR-006 | Signed-in public-only viewers need public control to act through dashboard scope, just as anonymous viewers do. Owners and direct/group-shared users can operate permitted writable bindings. | [scope resolution](../pkg/dashscope/request.go) |
| 008 FR-004, FR-009 | Port types and field controls remain after unload. Ownership collisions are rejected for widget packages and AI providers; the other type registries do not enforce cross-plugin uniqueness. | [plugin host](../pkg/pluginhost/host.go), [widget loader](../pkg/widgets/loader.go), [AI providers](../pkg/integrations/ai/providers.go), [node registry](../pkg/nodeapi/registry.go) |
| 009 plan, 010 FR-006, FR-009, SC-004 | No replay applies to resident connections; ordinary helpers retry once on peer loss. Agent command/script nodes disable their execution retries. macOS session-context actions can run through helpers while credential checks remain in the parent. | [helper client](../automation/helper_client.go), [delegation](../automation/delegate.go), [resident session](../automation/resident.go), [agent host nodes](../agent/exec) |
| 010 plan | Stable-pixel waits use a 100 ms ticker. Backend latency can make the actual polling interval longer. | [CLI state waits](../cmd/karakuri-auto/state.go) |
| Former 013, now [module specs](../automation/specs/README.md) | The library has its own import/dependency, caller ownership, options, discovery, error, token, helper, and authenticated connection contracts. The CLI owns command parsing, waits/evidence, and resident installation. The module index maps the former requirement IDs to their new features. | [module](../automation/go.mod), [composition](../automation/automation.go), [public API](../automation/autoapi), [discovery](../automation/desktopenv), [transport](../automation/resident.go) |

The flow and plugin guides now describe the same validation/unload limits.
The automation guide and module README state helper replay and cancellation
semantics and link the library contract. Requirement IDs in root features 001
through 012 remain stable. The former root module feature's requirements are
mapped in the module index.

## Review limits

This is a targeted source audit, not a requirement-by-requirement conformance
certificate for every feature. The remaining baseline requirements still need
their acceptance checks when those features change. Existing tests can verify
their fixtures without covering all claims in a spec.

Platform desktop behavior depends on actual compositor/provider support and OS
grants. The library contract distinguishes local calls, ordinary helpers, and
resident connections, but this audit does not certify every path on every OS.
In particular, helper retry can repeat an uncertain action, and canceling a
helper caller's wait does not send per-call cancellation to the remote operation.
Those are current transport limits, not new reliability promises.

The separate module needs its own specification boundary because other Go
applications can import it without the Karakuri CLI or agent. Its contracts now
live under `automation/specs/` and share the root constitution and workflow;
no nested toolkit or invented historical task list is needed.
