# Design plan: [Feature name]

**Feature**: `[repository-relative feature directory]` | **Date**: [Date] | **Spec**: [spec.md](spec.md)

## Summary

[Requirements and technical approach. Read the relevant feature guide first.]

## Technical context

- Language and dependencies: [Repository versions and existing dependencies.]
- Components: [Concrete directories and boundaries.]
- Storage: [Persistent and transient state.]
- Limits: [Existing limits or justified new ones; no invented targets.]

## Constitution check

- [Failure routing, retry side effects, cancellation, bounded work.]
- [Draft/deploy behavior and shared REST/MCP/editor rules.]
- [Permissions, sandbox, agent trust, dashboard scope.]
- [Effect on databases, enrolled identities, CA material, encryption keys.]
- [Relevant Makefile targets and live checks.]

## Design and contracts

[Control flow, inputs/outputs, validation, authorization, errors.]

## State and data model

[Entities, ownership, transactions, migrations, lifecycle.]

## Verification

[Map scenarios to existing or required tests and live checks. Use `make dev`
and `make test`; restart and verify the development service.]

## Risks and open decisions

[Concrete limitations and questions. State explicitly when there are none.]

Generate research.md, data-model.md, contracts/, and quickstart.md when the
change needs them. Generate tasks.md for actual implementation work. Preserve
the existing baseline plan when extending it.
