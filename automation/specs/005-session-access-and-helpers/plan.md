# Design plan: Session access and helpers

**Feature**: `automation/specs/005-session-access-and-helpers` | **Date**: 2026-10-04
**Spec**: [spec.md](spec.md) | **Status**: Implemented baseline

## Summary

Discover graphical environments without changing the parent process, then use
caller-configured same-executable helpers when detached or privileged callers
need a graphical user's context. Keep helper retry limits and session authority explicit.

## Technical context and design

`desktopenv` verifies live sockets and reads graphical process environments.
Linux privileged selection uses session-manager/NSS data; FreeBSD scans live
graphical process environments. macOS selects GUI sessions and starts a temporary
launchd job using the same executable. Windows LocalSystem locates the active
console user and uses its session token for helper startup. An administrator
account alone cannot obtain that delegation path.

`HelperCommand` opts callers into re-execution. The executable must dispatch
that argv to its helper handler. Helpers start lazily. Ordinary startup repeats
target discovery, permitting boot-before-login composition and later replacement.
A failed lookup can use a usable startup target; a pending target with no
graphical user returns an error. macOS per-session helper factories also reach
explicit target GUI domains for session actions.

## State and data model

Delegate targets contain identity, session metadata, and a complete child
environment. Helpers, connections, and launchd jobs are transient. Parent
compositions register helper closers; macOS cleanup uses a separate bounded
context so a canceled requesting call does not prevent job removal. Restore
tokens and signing grants remain persistent caller/host state under feature 001.
Each helper handler chooses its own child-session options and token storage.

## Contracts and failure handling

The helper client correlates framed JSON requests and replies by ID. Ordinary
calls retry once after peer loss while caller context remains active. An action
whose reply was lost can repeat. Canceling the caller's wait forgets its reply
slot without sending remote per-call cancellation/deadlines. Helper/session
close releases the owned transport and child lifecycle. Error flattening retains
unsupported/missing-tool categories; other backend errors become diagnostic text.

Session listing and credential checks stay in the parent. macOS session-context
verbs and credential typing use helper actors/keyboards where its GUI domain
requires them. Credentialed login verifies the account before supported
switch/unlock; it does not create a new user login. Windows locked-console typing
uses configured secure-input handling as LocalSystem in the target session.

The Linux/FreeBSD CLI can adopt discovered environment in its own process.
Library discovery never rewrites that environment. Privilege does not replace
desktop bus ownership, portal consent, macOS signing, or privacy grants.

Windows SYSTEM detection opens the process token explicitly because Windows 7
lacks the current-process pseudo-token handle. Session lock detection corrects
the reversed WTS lock flags reported by Windows 7 and Server 2008 R2.

## Constitution check

- Account selection, consent, uncertain replay, and cancellation limits remain explicit.
- The split changes no helper wire, credential, signing, token, or agent identity format.
- Preserve authorized executables and use the Makefile plus documented host checks.

## Verification

Run `make dev TARGET=auto` and `make test TARGET=automation` from the repository
root; use agent targets for integration changes. Existing discovery/target,
helper round-trip, Unix account-check, and applicable Windows session fixtures
cover source contracts. Verify actual helper identity, lazy startup, input,
session actions, and cleanup on changed platforms using `AGENTS.md` hosts.
Use the persistent Mac signing identity and patched Windows 7 toolchain.
This baseline does not certify every live session or cancellation path.

## Risks and limits

Ordinary helper actions can repeat and outlive canceled caller waits. Session
lookup can change between observations, and fallback targets can become stale.
OS policy and grants can still deny operations in the correctly selected session.

## References

- [Discovery fixtures](../../desktopenv/desktopenv_test.go)
- [Target fixtures](../../delegate_test.go)
- [Helper transport fixtures](../../helper_test.go)
- [Linux delegation](../../delegate_linux.go)
- [FreeBSD delegation](../../delegate_freebsd.go)
- [Mac delegation](../../delegate_darwin.go)
- [Windows delegation](../../delegate_windows.go)
- [Wire error handling](../../helper_wire.go)
- [Account verification](../../internal/unixauth/verify.go)

## Desktop mutation delivery

Desktop proxies in desktop_helper.go and inspection mutations call helper.callOnce. Read-only methods continue to use helper.call. ServeHelper composes localDesktop around the local session before dispatching desktop requests.
