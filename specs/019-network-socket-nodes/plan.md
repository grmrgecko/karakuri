# Design plan: Network socket nodes

**Feature**: `specs/019-network-socket-nodes` | **Date**: 2026-10-06 | **Spec**: [spec.md](spec.md)

## Summary

Register seven core nodes and three settings-owned connection kinds. Add the
shared TCP/UDP/WebSocket drivers in `pkg/integrations/socket` and nodes in
`pkg/nodes/core`. Keep session objects outside message envelopes.

## Technical context

- Go 1.26.5; existing coder/websocket 1.8.15 and x/net ipv4/ipv6 support.
- Existing connmgr owns encrypted configuration, startup, replacement, status
  and shutdown. Core plugin registers kinds; catalog forms need no bespoke UI.
- Frames, sockets and subscriptions are transient. No database schema change.
- Maximum frame/response 1 MiB, default 64 KiB; UDP maximum 65507 bytes. At most
  64 sessions and 64 subscribers per connection. Queues hold at most 32 frames
  and 4 MiB per subscriber. Network operations have context-aware deadlines.

## Constitution check

- Action errors use existing failure routing and explicit no-retry errors.
  Sends may be partial; no offline queue, resend or delivery guarantee is added.
- Catalog metadata drives editor and service validation. Draft edits do not
  reconfigure settings connections or deployed flows.
- Flow authors retain LAN access like HTTP nodes. Listener endpoint policy is
  configured explicitly. Sandbox and agent permissions are unchanged.
- Existing encryption, records, identities and CA material remain readable.
- Verify with Makefile targets, real local peers, MCP and browser checks.

## Design and contracts

Connection `tcp` supports client, listener and request-only modes. Framing is
connection-owned for the single persistent reader: chunks, delimiter, fixed
length or orderly close. Delimiter/fixed partial EOF is an error. Request-only
mode owns configuration and readiness without opening an idle session.
TCP Request opens a fresh socket using that connection's endpoint/TLS settings,
sends once and reads by delimiter, fixed length, close or idle. Its absolute
deadline includes connect, TLS, write and read. An idle gap succeeds only after
some response bytes and before the absolute deadline.

Connection `udp` binds one socket used by input and output nodes. Configure
family, bind address, default destination, multicast group/interface,
TTL/hop-limit and loopback. IPv4 supports explicit broadcast configuration.
Replies validate the connection identity and use incoming remote address.

Connection `websocket` supports client and dedicated HTTP(S) listener modes.
Client configuration owns URL, secret headers and subprotocols. Listener
configuration owns bind address, exact path, origins and optional bearer token.
TLS configuration uses inline PEM, never arbitrary filesystem paths. Private
keys and handshake credentials are masked through existing connection secrets.

Every persistent session gets a UUID and serialized, deadline-bound writes.
Output selects client peer, exact incoming session or explicit broadcast.
Disconnected/stale/foreign sessions fail without selecting another target.
Frames include connection/session IDs, peer/local addresses and frame type.
Input nodes choose text/binary/base64/JSON decoding, topic and test payload.
JSON imports only the payload. Decode errors log and drop; action errors route.

Drivers serialize subscription changes and queue sends. Protocol readers never
wait on flow execution. Closing a connection closes subscriptions and discards
pending old frames; triggers resolve and subscribe to the replacement. Flow
disarm closes only its subscription. Client supervisors reconnect with bounded
backoff; listener bind failures return synchronously. Stop closes resources,
including upgraded WebSockets, and waits for owned goroutines.

## State and data model

See [data-model.md](data-model.md) and [contracts/nodes.md](contracts/nodes.md).
Connection configuration is immutable within a driver generation. No migration
or durable network state is introduced.

## Verification

Use real localhost TCP/UDP/TLS/WebSocket peers for framing, request isolation,
reply targeting, multicast, resource release and protocol bounds. Drive actions
through the real engine and triggers through real driver subscriptions. Use
existing real SQLite connection lifecycle fixtures where they verify replacement.
Run `make dev`, `make test TARGET=server` and `make test TARGET=frontend`. The real-peer
suite runs under `make test-integration`.
Restart/check the development unit, exercise MCP and browser-derived formats,
and remove disposable connections/flows after checks.

## Risks and open decisions

There are no unresolved design choices. Socket writes and listener broadcasts
may be partially delivered. UDP writes do not prove receipt. Native TCP chunks
have no application framing. Incoming queues and flow admission may drop events
with observable logs; no persisted recovery or exactly-once claim is made.
