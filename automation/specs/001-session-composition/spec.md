# Feature specification: Session composition and ownership

**Feature**: `automation/specs/001-session-composition`
**Created**: 2026-10-04
**Status**: Implemented baseline
**Input**: Give Go callers one desktop API with explicit backend availability,
caller-owned lifetime, diagnostics, and restore-token storage.

## User scenarios and testing

### User story 1: Compose the available capabilities (Priority: P1)

A caller creates a session and uses its interfaces without testing whether each
capability field is nil.

**Why this priority**: The public API must work on hosts with different desktop support.
**Independent test**: Compose with zero-value options and inspect every capability field
and the environment description.

**Acceptance scenarios**:

1. **Given** a supported host, **When** `New` succeeds, **Then** every capability field
   contains a backend or an error-returning stub.
2. **Given** zero-value options, **When** a session is composed, **Then** the application
   identity is `karakuri`, no restore tokens persist, and helper delegation is disabled.
3. **Given** an unavailable capability, **When** it is called, **Then** the caller gets a
   diagnostic error instead of a nil-interface failure.

### User story 2: Own resources and distinguish failures (Priority: P1)

A host application keeps its session alive for the required work, then releases
resources and handles unsupported operations separately from absent utilities.

**Why this priority**: Resource cleanup and actionable failures belong to the caller.
**Independent test**: Check error categories and close a composition with multiple
registered resources, including a closer that returns an error.

**Acceptance scenarios**:

1. **Given** an absent backend utility, **When** an operation needs it, **Then**
   `IsMissingTool` identifies the error and its text names the dependency.
2. **Given** an operation the backend cannot support, **When** it is attempted,
   **Then** `errors.Is(err, ErrUnsupported)` identifies that category where reported.
3. **Given** an earlier closer fails, **When** the caller closes the session,
   **Then** every registered closer is attempted and the first error is returned.

### User story 3: Persist backend restore tokens (Priority: P2)

A caller chooses where opaque backend tokens persist without involving server
secrets or agent identity storage.

**Why this priority**: Portal restoration can survive application runs while remaining
under the composing application's control.
**Independent test**: Use a disposable file store, save and load a token, and inspect
the created paths and Unix creation modes.

**Acceptance scenarios**:

1. **Given** a newly constructed file store, **When** no save has occurred, **Then**
   the store has created no directory or token file.
2. **Given** a saved nonempty token, **When** it is loaded, **Then** its surrounding
   whitespace is trimmed; unreadable or empty tokens are reported as absent.

### Edge cases

- Describe output records composition, including helper transport, rather than a
  live query of the helper's eventual backend or current graphical user.
- Close has no general concurrency or idempotence guarantee across all backends.
- File token storage uses safe filenames; distinct keys can map to the same filename
  after unsupported characters are replaced.
- File creation modes do not repair permissions on existing directories or files.

## Requirements

### Functional requirements

- **FR-001**: The module MUST be independently importable without server, agent identity,
  database, or CLI dependencies.
- **FR-002**: A successfully composed session MUST populate `Inspection`, `Keyboard`,
  `Mouse`, `Screen`, `Window`, `Clipboard`, `Notifier`, `Dialog`, `Speaker`, `Media`,
  `Power`, `Idle`, `Opener`, and `Sessions`; unavailable capabilities MUST return errors.
- **FR-003**: `Describe` MUST report the composed OS, display, compositor, capability
  backends or helper transport, and delegation target when present.
- **FR-004**: Zero-value `Options` MUST be valid. Empty `AppID` MUST use `karakuri`,
  diagnostics through `Logf` MUST be optional, and empty helper argv MUST disable
  delegation.
- **FR-005**: The caller MUST own session lifetime. `Close` MUST attempt all registered
  closers and return the first cleanup error.
- **FR-006**: The API MUST provide `ErrUnsupported` and `IsMissingTool` so callers can
  distinguish unsupported operations and absent utility dependencies from other errors.
- **FR-007**: Restore-token storage MUST be caller-controlled through `TokenStore`;
  nil MUST disable persistence. Storage MUST NOT manage agent identities or server
  encryption state. Helper handlers MUST configure their own child-session storage.
- **FR-008**: `FileTokenStore` MUST create directories lazily on save, sanitize token
  filenames, use Unix creation modes 0700/0600, and load only nonempty trimmed tokens.
- **FR-009**: Backend composition MUST support Linux, FreeBSD, macOS, and Windows without
  cgo, subject to available providers, utilities, permissions, and OS grants.

### Key entities

- Session: non-nil capability interfaces and registered resource closers.
- Environment: composition-time host, backend, and delegation description.
- Options: application identity, diagnostics, token store, and helper entry points.
- Token store: caller-selected persistence for opaque backend restore tokens.

## Success criteria

- **SC-001**: Zero-option composition leaves every capability field populated and
  launches no delegated helper.
- **SC-002**: Unsupported and missing-tool fixtures expose their respective categories;
  closing a session attempts every registered closer despite an earlier error.
- **SC-003**: A file store creates no paths before save and loads nonempty trimmed tokens
  from the caller's chosen directory.

## Assumptions and scope

This feature owns composition, public interfaces, options, error categories,
cleanup, and restore-token storage. Capability behavior is defined by the other
[module specs](../README.md). Successful composition does not certify every
operation on the current desktop.

## References

- [Design plan](plan.md)
- [Module guide](../../README.md)
- [Composition](../../automation.go)
- [Public interfaces](../../autoapi/interfaces.go)
- [Errors](../../autoapi/errors.go)
- [Token storage](../../autoapi/tokenstore.go)
- [Module dependencies](../../go.mod)
