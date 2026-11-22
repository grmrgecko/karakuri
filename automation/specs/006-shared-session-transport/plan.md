# Design plan: Shared session transport

**Feature**: `automation/specs/006-shared-session-transport` | **Date**: 2026-10-04
**Spec**: [spec.md](spec.md) | **Status**: Implemented baseline

## Summary

Share a composed session across authenticated connections. Keep the shared
session under host ownership and fail uncertain remote actions without replay.

## Technical context and design

`NewSessionServer` wraps an existing `Session`. The host authenticates each
`net.Conn` and passes it to `Serve`. `NewRemoteSession` wraps an authenticated
connection in helper capability adapters, sets `noRetry`, and obtains the
server's description before returning. Initialization failure closes the client
connection. These APIs create no listener and install no resident.

Each served connection processes requests against the shared session with its
own context. Disconnect or parent cancellation closes the connection and cancels
that context. Keyboard and mouse calls acquire a shared input gate across all
clients. Other capabilities do not take that gate; sequences of input calls
are not atomic transactions.

## State and data model

The host owns the desktop session and closes it after its serving calls finish.
Each client owns its connection and correlated pending replies. Closing a remote
session closes that connection, never the shared desktop session. Stored tokens
and persistent backend resources belong to the session owner under feature 001.

## Contracts and failure handling

`noRetry` disables the ordinary helper's peer-loss retry. A lost reply fails
even if the action already executed. Caller cancellation drops the entire
client connection; other pending calls on it can fail too. The server cancels
connection work, but backend cancellation is cooperative and cannot undo input
already delivered.

Framed requests preserve capability/method arguments and request IDs. Wire
errors retain unsupported/missing-tool categories and diagnostic text. Captures
use PNG encoding; idle calls carry the explicit supplied time, while text
typing uses `RealClock` at the receiver. The CLI adds socket/pipe authentication,
command queues, output caps, timeouts, and resident lifecycle under root feature 010.

## Constitution check

- The host must authenticate connections and protect its listener.
- Lost replies cannot trigger implicit action replay.
- This split changes no wire format, token, or resident installation; use Makefile checks.

## Verification

Run `make dev TARGET=auto` and `make test TARGET=automation` from the repository
root. Existing resident fixtures exercise connection sharing, input serialization,
and cancellation; helper fixtures cover wire values, images, and error kinds.
Use the relevant CLI live-host checks when socket/pipe forwarding or persistent
clipboard ownership changes. This baseline adds no new live-host certification.

## Risks and limits

Authentication is supplied by the host rather than enforced by this API. A
connection cancellation can fail several pending client calls. No replay prevents
duplication but cannot determine or reverse an action completed before peer loss.

## References

- [Session transport](../../resident.go)
- [Client transport](../../helper_client.go)
- [Protocol](../../internal/hrpc/hrpc.go)
- [Resident fixtures](../../resident_test.go)
- [Helper fixtures](../../helper_test.go)
- [CLI resident design](../../../specs/010-desktop-automation/plan.md)

## Native desktop actions

NewRemoteSession wires helperDesktop. SessionServer serializes desktop click_control and invoke_menu requests with keyboard/mouse input, and dispatches desktop observations through the same connection.

## Drag and placement transport

desktop_helper.go sends drag/place_window with callOnce. SessionServer's input
gate includes those calls and window actions. Local adapters hold another gate
around composed input. Ordinary helper serveSession shares a SessionServer across
its requests and cancels/waits for active work on EOF before closing its session.
The disconnect fixture uses two authenticated net.Pipe clients and observes
button release before queued input proceeds.
