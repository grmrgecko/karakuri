# Automation module specifications

These specs define the public contracts of
`github.com/grmrgecko/karakuri/automation`. Each feature has a `spec.md` for
observable requirements and a `plan.md` for the implemented design. They
describe the source baseline at `5158025`, including the corrections recorded
in the [source audit](../../specs/code-audit.md). They are not a completed
cross-platform acceptance report.

## Feature index

| Feature | Specification | Design plan | Owns |
| --- | --- | --- | --- |
| 001 Session composition | [Requirements](001-session-composition/spec.md) | [Design](001-session-composition/plan.md) | Public API, options, backend selection, errors, cleanup, restore tokens |
| 002 Desktop input | [Requirements](002-desktop-input/spec.md) | [Design](002-desktop-input/plan.md) | Keyboard/mouse, key parsing, clocks, portal pointer geometry |
| 003 Capture and inspection | [Requirements](003-capture-and-inspection/spec.md) | [Design](003-capture-and-inspection/plan.md) | Images, windows/actions, accessible elements, displays/modes |
| 004 Desktop services | [Requirements](004-desktop-services/spec.md) | [Design](004-desktop-services/plan.md) | Clipboard, notifications/dialogs, speech, media/power, idle, opening |
| 005 Session access and helpers | [Requirements](005-session-access-and-helpers/spec.md) | [Design](005-session-access-and-helpers/plan.md) | Discovery, account selection, helper lifecycle/transport, session actions, secure input |
| 006 Shared session transport | [Requirements](006-shared-session-transport/spec.md) | [Design](006-shared-session-transport/plan.md) | Authenticated connections, shared resource ownership, input serialization, no replay |

## Boundaries and workflow

The [module README](../README.md) is the usage guide. Root
[feature 010](../../specs/010-desktop-automation/spec.md) owns `karakuri-auto`
commands, window matching, snapshots, state waits, evidence, command files, and
resident installation. Root [feature 009](../../specs/009-remote-agents/spec.md)
owns agent enrollment and remote command execution. The library provides the
capabilities those consumers use.

Use the shared root [Spec Kit workflow](../../specs/spec-kit.md),
[constitution](../../.specify/memory/constitution.md), scripts, templates, and
Codex skills. There is no module-local `.specify/` installation. Feature numbers
are independent of root `specs/`; cite a full path such as
`automation/specs/005-session-access-and-helpers/FR-007` when discussing both scopes.
Shared-session calls never replay a lost reply; ordinary helper calls can retry
once. Keep those transport contracts separate when editing a consumer spec.

Select an existing feature from the repository root with:

```sh
export SPECIFY_FEATURE_DIRECTORY=automation/specs/005-session-access-and-helpers
.specify/scripts/bash/check-prerequisites.sh --paths-only --json
```

Preview creation in this module with:

```sh
.specify/scripts/bash/create-new-feature.sh --scope automation --dry-run --json \
  --short-name example-feature 'Describe the proposed library behavior'
```

Build and test from the repository root with `make dev TARGET=auto` and
`make test TARGET=automation`. Follow `AGENTS.md` for the service handoff and
changed desktop backends. Acceptance scenarios describe checks to perform;
this reorganization adds no live-host evidence or historical task lists.

## Requirement relocation

The former root `013-automation-module` feature is split here. The mapping below
keeps its 23 requirement references traceable after removal of that aggregate
spec. Requirement IDs are local to each new feature; root features 001 through
012 retain their existing IDs.

| Former 013 requirement | Current module requirements |
| --- | --- |
| FR-001 | [001](001-session-composition/spec.md) FR-001, FR-009 |
| FR-002 | [001](001-session-composition/spec.md) FR-002 |
| FR-003 | [001](001-session-composition/spec.md) FR-003 |
| FR-004 | [001](001-session-composition/spec.md) FR-004, FR-007 |
| FR-005 | [001](001-session-composition/spec.md) FR-005 |
| FR-006 | [001](001-session-composition/spec.md) FR-006; [005](005-session-access-and-helpers/spec.md) FR-010; [006](006-shared-session-transport/spec.md) FR-005 |
| FR-007 | [005](005-session-access-and-helpers/spec.md) FR-001 |
| FR-008 | [005](005-session-access-and-helpers/spec.md) FR-002 |
| FR-009 | [005](005-session-access-and-helpers/spec.md) FR-003 |
| FR-010 | [005](005-session-access-and-helpers/spec.md) FR-004 |
| FR-011 | [005](005-session-access-and-helpers/spec.md) FR-005 |
| FR-012 | [005](005-session-access-and-helpers/spec.md) FR-006 |
| FR-013 | [005](005-session-access-and-helpers/spec.md) FR-007 |
| FR-014 | [006](006-shared-session-transport/spec.md) FR-001, FR-004 |
| FR-015 | [006](006-shared-session-transport/spec.md) FR-002, FR-003 |
| FR-016 | [002](002-desktop-input/spec.md) FR-001, FR-002, FR-004; [004](004-desktop-services/spec.md) FR-006 |
| FR-017 | [002](002-desktop-input/spec.md) FR-003, FR-005, FR-006 |
| FR-018 | [003](003-capture-and-inspection/spec.md) FR-001, FR-002, FR-003 |
| FR-019 | [003](003-capture-and-inspection/spec.md) FR-004, FR-005, FR-006, FR-007 |
| FR-020 | [004](004-desktop-services/spec.md) FR-001 through FR-005, FR-007 |
| FR-021 | [005](005-session-access-and-helpers/spec.md) FR-008 |
| FR-022 | [005](005-session-access-and-helpers/spec.md) FR-009 |
| FR-023 | [001](001-session-composition/spec.md) FR-007, FR-008 |
