# Tasks: [Feature name]

**Feature**: `[repository-relative feature directory]`
**Input**: spec.md and plan.md in that directory, plus any research, data model,
contracts, or quickstart documents required by the change.

Use the selected feature's actual path under `specs/` or `automation/specs/`.
Generate tasks for pending work. Do not invent completed baseline tasks or
initialize an existing project again. Replace the examples and placeholders.

## Task format

Every task uses a checkbox and sequential `T###` ID. Add `[P]` only when its
files and dependencies permit parallel execution. Add `[US1]`, `[US2]`, or the
corresponding story label to story work. Include repository-relative source paths.
Use meaningful contract/regression tests when the change needs them; avoid
tests that mirror implementation or verify documentation wording. Size each
test to the shortest run that demonstrates its requirement; add soak or
multi-hour runs only when the spec requires that duration.

## Phase 1: Required preparation

- [ ] T001 [Concrete prerequisite from the plan, with its actual path]

## Phase 2: User story 1

**Goal**: [The independently verifiable outcome from the spec]
**Independent check**: [The acceptance path establishing that outcome]

- [ ] T002 [US1] [Implement the required behavior in its actual source path]

Add further story phases in priority and dependency order. Shared prerequisites
must precede dependent work. State which phases can proceed independently.

## Final verification and documentation

- [ ] T003 [Run relevant make dev and make test targets, plus required live checks]
- [ ] T004 [Update the feature guide, spec, and plan to match final behavior]
- [ ] T005 [Restart and verify karakuri-dev.service and the HTTP listener per AGENTS.md]

## Dependencies and execution order

[Explain prerequisite and story dependencies. Mark real parallel opportunities
without authorizing sub-agents, branches, or external actions.]
