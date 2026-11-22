# Feature specification: Network socket nodes

**Feature**: `specs/019-network-socket-nodes`
**Created**: 2026-10-06
**Status**: Implemented
**Input**: Add TCP In, TCP Out, TCP Request, UDP In, UDP Out, WebSocket In and WebSocket Out.

## User scenarios and testing

### User story 1: Exchange messages with TCP devices (Priority: P1)

A flow receives framed device events, replies on the originating session, or sends a request and reads its response.

**Why this priority**: Custom LAN devices often expose TCP instead of HTTP or MQTT.
**Independent test**: Real local peers fragment frames, connect two clients, exchange replies and run concurrent requests.

**Acceptance scenarios**:

1. Given a TCP client or listener connection, when a complete configured frame arrives, then TCP In starts a run with the decoded payload and peer/session metadata.
2. Given two connected peers, when TCP Out replies to an incoming message, then only that session receives it; broadcast requires explicit selection.
3. Given concurrent TCP requests, when distinct responses arrive, then each execution receives only its own response and preserves its topic and metadata.
4. Given an incomplete, oversized or late response, when the request ends, then the error branch runs without replaying the request or emitting a partial success.

### User story 2: Exchange UDP datagrams (Priority: P1)

A flow receives one event per datagram and sends unicast, broadcast or multicast packets, including replies from the listening source port.

**Why this priority**: UDP carries discovery and device telemetry that existing nodes cannot consume.
**Independent test**: Real IPv4/IPv6 local sockets exchange binary and empty datagrams and verify source addresses.

**Acceptance scenarios**:

1. Given a bound UDP connection, when a datagram arrives, then UDP In emits one message with its peer address, including empty datagrams.
2. Given an incoming datagram, when UDP Out replies through the same connection, then the sender receives one packet from the original listening port.
3. Given an explicitly configured multicast group/interface or IPv4 broadcast destination, when a datagram is sent or received, then the selected transport settings apply; unavailable interfaces and invalid address families fail clearly.

### User story 3: Exchange WebSocket messages (Priority: P1)

A flow connects to a live API or hosts a dedicated WebSocket endpoint with input/output nodes sharing the same session.

**Why this priority**: Live APIs need native WebSocket message boundaries and bidirectional sessions.
**Independent test**: Real WebSocket client/listener peers exchange text and binary messages, targeted replies and broadcasts.

**Acceptance scenarios**:

1. Given a client or listener connection, when a complete WebSocket message arrives, then WebSocket In emits the chosen format and frame/session metadata.
2. Given several listener clients, when a targeted reply or explicit broadcast runs, then the selected recipients receive the message.
3. Given configured listener credentials, origin policy and path, when a handshake violates them, then it is refused without exposing credentials.

### User story 4: Manage socket connections reliably (Priority: P1)

Settings owns connection configuration and encrypted credentials; flows subscribe without owning or duplicating sockets.

**Why this priority**: Shared input/output nodes must survive settings replacement and close cleanly.
**Independent test**: Disconnect, replace, disable and restart real connections while subscriptions and reads are active.

**Acceptance scenarios**:

1. Given a disconnected client, when the peer returns, then the connection reconnects and input subscriptions continue with new session IDs.
2. Given a replaced or disabled connection, when its old session ID is used, then the send fails and never falls back to another peer or broadcast.
3. Given a flow disarm or connection stop, when shutdown completes, then its subscriptions or sockets are released and listener ports can be reused.
4. Given a declared payload format, when it changes in the editor, then the port type updates live and matches server validation and MCP metadata.

### Edge cases

- TCP chunk boundaries are arbitrary. Delimiters may cross reads; multiple frames may share one read. Fixed lengths count bytes. Partial delimiter/fixed frames at EOF are errors.
- Bounds cover frame bytes, sessions, subscriptions, queued delivery bytes, response duration and write duration. Overflow is observable and cannot grow memory without limit.
- Invalid payload encodings, malformed JSON, invalid TLS credentials, unavailable peers, stale sessions and bind failures produce actionable errors. Trigger decode errors are logged and dropped.
- Cancellation closes an isolated request socket and interrupts active writes. Partial writes and broadcasts can leave delivery uncertain and must not retry automatically.
- IPv6 has multicast but no broadcast. UDP receipt and successful writes do not promise remote application delivery.
- In-flight sessions, queued messages and requests do not recover after process restart. Input triggers retain normal flow run admission limits.

## Requirements

### Functional requirements

- **FR-001**: The catalog MUST expose all seven requested server node types, with common message outputs and ordinary error branches on actions.
- **FR-002**: TCP and WebSocket connections MUST support persistent clients and dedicated listeners shared by input/output nodes. TCP MUST also support a request-only endpoint without a persistent socket.
- **FR-003**: TCP In MUST support chunks, delimiter, fixed-byte-length and connection-close framing, bounded frame sizes and text, binary, base64 or JSON payloads.
- **FR-004**: TCP Out and WebSocket Out MUST support client sends, exact-session replies and explicit listener broadcasts. Retired or foreign reply sessions MUST fail without fallback.
- **FR-005**: TCP Request MUST isolate each exchange, support delimiter, fixed-length, connection-close and idle termination, enforce an overall deadline and response size, and preserve the original envelope while replacing its payload.
- **FR-006**: UDP In/Out MUST support IPv4/IPv6 unicast and multicast, explicit interface and multicast TTL/loopback settings, IPv4 broadcast and same-source-port replies. Each send MUST remain one datagram.
- **FR-007**: WebSocket In/Out MUST preserve native message boundaries and support text, binary, base64 and payload JSON. They MUST NOT import remote execution IDs, group stacks or continuations.
- **FR-008**: TCP/WebSocket MUST support TLS with inline CA/certificate/private-key settings. WebSocket clients MUST support configured headers/subprotocols; listeners MUST enforce path/origin policy and optional bearer authentication. Secrets MUST use existing encrypted storage and masking.
- **FR-009**: Connections MUST reconnect clients, expose lifecycle status, bound resources and close sockets/subscriptions on replacement, disablement or shutdown. Input triggers MUST resubscribe after replacement and stop emitting on disarm.
- **FR-010**: Socket actions MUST honor cancellation and bounded deadlines and MUST NOT replay uncertain sends through automatic retry, even when a graph requests retries.
- **FR-011**: Settings, editor, REST and MCP MUST use the same catalog and validation. Existing database records, encrypted secrets, enrolled identities and graph documents MUST remain readable.

### Key entities

- Socket connection: settings-owned identity, protocol, endpoint, limits and encrypted credentials.
- Session: transient connection-owned peer and opaque identity replaced on reconnect.
- Frame/datagram: bounded bytes and transport/peer metadata delivered to subscriptions.
- Subscription: flow-owned bounded delivery queue, invalidated on connection replacement.
- Request exchange: execution-owned isolated socket and absolute response deadline.

## Success criteria

- **SC-001**: Real TCP peers establish framing across fragmented/coalesced writes, targeted versus broadcast sends, independent concurrent responses and failed partial/late responses.
- **SC-002**: Real UDP peers establish exact datagram boundaries, empty/binary payloads, same-port replies and supported family/multicast settings.
- **SC-003**: Real WebSocket peers establish shared client/listener sessions, targeted sends, binary frames and handshake policy enforcement.
- **SC-004**: Replacement/cancellation checks release resources, retain working subscriptions and reject old session IDs without replay.
- **SC-005**: Relevant Makefile builds/tests, MCP functional checks and live editor derivation checks pass; the development service remains active and answers HTTP requests.

## Assumptions and scope

- Nodes run on the server. No remote-agent socket nodes are included.
- WebSocket listeners bind a dedicated configured port; they do not replace the application's authenticated event socket.
- Trusted flow authors can reach LAN endpoints, like existing HTTP/MQTT nodes. Script sandbox policy is unchanged.
- New connection kinds use existing encrypted records. No schema migration, key regeneration or identity changes are needed.
- Importing flows from other automation tools is outside scope.

## References

- [Connection requirements](../004-connections-devices/spec.md)
- [Execution requirements](../003-flow-execution/spec.md)
- [Flow guide](../../docs/flow-authoring.md)
