# Feature specification: Session access and helpers

**Feature**: `automation/specs/005-session-access-and-helpers`
**Created**: 2026-10-04
**Status**: Implemented baseline
**Input**: Let opted-in detached or privileged callers reach the correct graphical
user and perform supported session actions without assuming that privilege grants
desktop access.

## User scenarios and testing

### User story 1: Reach the appropriate graphical session (Priority: P1)

A caller opts into a helper in its own executable so desktop I/O runs with the
selected graphical user's environment and OS identity.

**Why this priority**: Services and SSH processes can be outside the desktop's bus or
window-server context even when their OS account has sufficient privileges.
**Independent test**: Discover a live session, configure a helper handler, and inspect
the helper's identity/environment on the documented host.

**Acceptance scenarios**:

1. **Given** a detached same-user Linux/FreeBSD caller with helper argv, **When**
   desktop I/O is requested, **Then** its helper uses that user's discovered desktop
   environment without changing the parent's process environment.
2. **Given** a delegated composition before graphical login, **When** a user signs in
   and a desktop call starts the helper, **Then** startup discovers the available user.
3. **Given** empty helper argv, **When** a session is composed, **Then** the module
   composes locally rather than re-executing an unconfigured command.

### User story 2: Handle helper loss and cancellation (Priority: P1)

A caller observes the actual helper transport's retry and cancellation limits.

**Why this priority**: Repeating uncertain desktop actions can produce duplicate effects.
**Independent test**: Exercise a lost helper reply and cancellation of a dispatched call
over controlled helper connections.

**Acceptance scenarios**:

1. **Given** an active caller context and helper peer loss, **When** its call fails,
   **Then** it retries once; an action completed before reply loss can run again.
2. **Given** a dispatched ordinary helper call, **When** the caller cancels its context,
   **Then** its wait ends without sending a per-call cancel to the helper operation.
3. **Given** an owned helper, **When** the owning session closes, **Then** its registered
   transport and helper cleanup are attempted.

### User story 3: Operate login sessions with appropriate authorization (Priority: P1)

A caller lists sessions and requests supported actions, including credentialed
switch/unlock, using the platform's account checks and required input path.

**Why this priority**: Session actions cross account and OS security boundaries.
**Independent test**: Check session metadata and permission failures, then use an
authorized disposable session for changed platform actions.

**Acceptance scenarios**:

1. **Given** a credentialed login request, **When** supported switch/unlock is performed,
   **Then** account verification happens before the credentialed action.
2. **Given** locked Windows credential typing without required LocalSystem/helper
   support, **When** unlock is requested, **Then** it reports the missing support.
3. **Given** a macOS action that needs GUI session context, **When** the privileged
   parent cannot perform it directly, **Then** the target helper handles the session
   actor or keyboard while credential checks remain in the parent.

### Edge cases

- Stale sockets and SSH-forwarded displays do not establish live local attachment.
- Describe reports composition-time delegation, not continuously refreshed session state.
- A failed session lookup can reuse a usable startup target; a pending target with no
  graphical user fails explicitly.
- Windows administrator status is insufficient for the LocalSystem delegation path.
- FreeBSD has no logind active-seat selection; it discovers graphical process environments.
- Existing user-session switch/unlock is narrower than unattended creation of a login.

## Requirements

### Functional requirements

- **FR-001**: Desktop discovery MUST return environment data without mutating process
  environment and distinguish live local attachment from stale/forwarded settings.
- **FR-002**: Delegation MUST require `Options.HelperCommand`, re-execute the composing
  executable with that argv, and expose helper handlers for its host to call. It MUST
  NOT require a resident installation.
- **FR-003**: Same-user Linux/FreeBSD and macOS SSH delegation MUST stay within the
  caller's graphical account. Root Unix delegation MUST select a non-root graphical
  user; Linux/macOS MUST prefer active then recent sessions, while FreeBSD MUST use
  discovered process environments. LocalSystem Windows MUST target the active console
  user; administrator status alone MUST NOT substitute for LocalSystem.
- **FR-004**: Ordinary helper startup MUST be lazy, re-discover its target on spawn,
  and support boot-before-login composition. A failed lookup MAY reuse a usable
  startup target; lack of a target MUST return a descriptive failure. macOS explicitly
  targeted per-session helpers MUST use their selected GUI domain.
- **FR-005**: macOS helpers MUST launch the same executable in the user's launchd GUI
  domain with private transport and temporary job cleanup. Hosts MUST preserve the
  authorized executable's signing identity across updates.
- **FR-006**: Delegated compositions MUST retain privileged session listing and
  credential verification in the parent; macOS session-context actions and unlock
  typing MUST use the target session's helper where required.
- **FR-007**: Ordinary helper calls MUST retry once on peer loss while caller context
  remains active. Cancellation MUST end the caller's wait without promising remote
  per-call cancellation or exactly-once action execution.
- **FR-008**: Session interfaces MUST list sessions and expose supported actions with
  OS permission checks. Credentialed login MUST verify the supplied account before
  supported switch/unlock; it MUST NOT promise creation of a new login session.
- **FR-009**: Locked Windows credential typing MUST require LocalSystem and configured
  `SecureInputCommand` handling; absent support MUST return an error rather than claim
  that typing succeeded.
- **FR-010**: Helper transport MUST preserve `ErrUnsupported`, `IsMissingTool`, and their
  diagnostic text. Other backend failures MUST reach the caller as error text.

### Key entities

- Desktop target: graphical user/session identity and full helper environment.
- Discovery result: verified display attachment and discovered desktop settings.
- Helper: lazy child process, framed calls, correlated replies, and cleanup owner.
- Session/login request: target identity, action, supplied account, and platform checks.
- Secure-input handler: host-provided Windows locked-console entry point.

## Success criteria

- **SC-001**: Opted-in helpers reach the selected user without rewriting parent
  environment; empty helper argv launches no delegate.
- **SC-002**: Controlled peer-loss calls perform at most one helper retry; canceled
  callers stop waiting without a claim that the remote operation stopped.
- **SC-003**: Session actions reject missing authorization/support, and macOS session
  typing/actions reach the selected GUI context while account checks stay privileged.
- **SC-004**: Error category fixtures survive helper transport and session close attempts
  the owned helper cleanup.

## Assumptions and scope

Same-user Linux/FreeBSD CLI attachment adopts environment in the command's own
process; that consumer behavior belongs to
[root feature 010](../../../specs/010-desktop-automation/spec.md).
Agent enrollment belongs to [root feature 009](../../../specs/009-remote-agents/spec.md).
Shared-session connections have the separate no-replay contract in
[006](../006-shared-session-transport/spec.md).

## References

- [Design plan](plan.md)
- [Module guide](../../README.md)
- [Discovery](../../desktopenv)
- [Delegation](../../delegate.go)
- [Helper client](../../helper_client.go)
- [Helper handlers](../../helper_serve.go)
- [Session contracts](../../autoapi/interfaces.go)
- [Windows secure input](../../secureinput_windows.go)

## Desktop mutation delivery

Native desktop control/menu actions, accessibility invocation, and display-mode changes use one helper attempt. Lost replies do not replay these operations. Other ordinary helper methods retain their existing retry-on-peer-loss contract. The desktop capability is composed inside helpers, including launchd-created macOS helpers and Windows console-session helpers.
