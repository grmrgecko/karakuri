# Feature specification: [Feature name]

**Feature**: `[repository-relative feature directory]`
**Created**: [Date]
**Status**: Draft
**Input**: [Requested behavior and purpose]

Read the constitution, relevant `docs/` guide, and existing specs first.
Use `specs/` for product features and `automation/specs/` for library contracts.
Describe observable requirements; put design in `plan.md`. Do not create a
branch unless requested. Replace placeholders before review.

## User scenarios and testing

### User story 1: [Outcome] (Priority: P1)

[Who needs this and what they do.]

**Why this priority**: [Concrete reason.]
**Independent test**: [Check demonstrating the outcome.]

**Acceptance scenarios**:

1. **Given** [State], **When** [Action], **Then** [Observable result].
2. **Given** [Failure], **When** [Action], **Then** [Recovery or refusal].

Add independently testable stories as needed with P1/P2/P3 priorities.

### Edge cases

- [Invalid input, concurrency, unavailable dependency, cancellation, or limit.]

## Requirements

### Functional requirements

- **FR-001**: The system MUST [Observable behavior].

### Key entities

- **[Entity]**: [Identity, state, ownership, relationships.]

## Success criteria

- **SC-001**: [Measurable outcome and check establishing it.]

Keep each criterion verifiable in minutes with the smallest duration, data set,
or load that would expose a failure. Do not set soak or endurance windows unless
the request names that duration.

## Assumptions and scope

- [Dependency or boundary; distinguish existing from proposed behavior.]

## References

- [Feature guide and related specs.]
