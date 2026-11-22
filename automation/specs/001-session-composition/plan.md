# Design plan: Session composition and ownership

**Feature**: `automation/specs/001-session-composition` | **Date**: 2026-10-04
**Spec**: [spec.md](spec.md) | **Status**: Implemented baseline

## Summary

Compose the available desktop backends in an independent Go module. Keep every
interface populated, expose composition diagnostics, and leave lifetime and
restore-token persistence with the caller.

## Technical context and design

`automation/go.mod` defines `github.com/grmrgecko/karakuri/automation` independently
of the root module. Public contracts live in `autoapi` and are re-exported by
`automation`; backends import `autoapi` to avoid a cycle. Platform builders use
native providers or utilities without cgo. `New` returns local capabilities or
delegated adapters under
[005-session-access-and-helpers](../005-session-access-and-helpers/plan.md).
Unavailable capabilities remain error-returning stubs.

`Describe` reports the environment stored at composition. Delegated capabilities
report `session-helper`; their eventual child backends and later user changes
are not continuously reflected. Callers should inspect operation errors even
after successful composition.

## State and data model

The session holds capability interfaces, environment data, and closer functions.
Backend connections and helpers are transient. `Close` attempts every registered
closer and returns its first error. Some backends hold no resources. Callers must
coordinate use and close; the API adds no universal concurrent-close guarantee.

`TokenStore` holds small opaque restore tokens. Nil disables persistence.
`FileTokenStore` creates a directory on its first save with mode 0700 and token
files with mode 0600 on Unix. Keys map to safe filenames, not unique encodings;
replaced characters can produce collisions. Load trims whitespace and treats
read errors or empty content as absence. Existing path permissions are not reset.
No database, agent identity, or server encryption key is accessed by this module.

## Contracts and failure handling

`Options` controls the application label, logging sink, token storage, helper
argv, and Windows secure-input argv. The default label is `karakuri`. Empty
helper argv prevents delegation. Child helper handlers choose their own options.
Missing-tool errors remain detectable with `IsMissingTool`; unsupported errors
match `ErrUnsupported`. Other errors carry the attempted operation's diagnostics.
Helper and shared-session transport preservation is specified in features 005/006.

## Constitution check

- Caller ownership and OS permissions remain explicit at composition boundaries.
- The spec split changes no runtime, token, identity, or encryption format.
- Use the shared [constitution](../../../.specify/memory/constitution.md) and
  repository Makefile; preserve caller-owned restore tokens during future changes.

## Verification

From the repository root, run `make dev TARGET=auto` and
`make test TARGET=automation`. Use agent targets if its composition changes.
Check zero-option composition, non-nil interfaces, error categories, cleanup,
and disposable token paths. Existing fixtures cover capability transport and
selection; they do not establish every backend's live availability.

This is a source-backed baseline, not evidence that every acceptance scenario
ran on every desktop host. Follow `AGENTS.md` for service and host checks.

## Risks and limits

Backend availability can change after composition. Callers own resource lifetime,
token filenames, existing path permissions, and persistent application identity.

## References

- [Module guide](../../README.md)
- [Composition](../../automation.go)
- [Public API](../../autoapi)
- [Platform selection](../../detect_unix.go)
- [Transport fixtures](../../helper_test.go)
