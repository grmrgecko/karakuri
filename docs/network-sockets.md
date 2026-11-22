# Network socket nodes

Add TCP, UDP, or WebSocket connections in **Settings, Connections**, then select one
in a Network node. Several flows can share a connection. Disabling a flow removes
its subscription. Disabling or editing the connection closes its sockets, and
input triggers subscribe again when the replacement starts.

| Node | Behavior |
| --- | --- |
| TCP In | Starts a run for each configured stream frame. |
| TCP Out | Writes to a client peer, one incoming session, or all listener sessions. |
| TCP Request | Opens a private socket, sends once, and reads one bounded response. |
| UDP In | Starts a run for each datagram, including empty datagrams. |
| UDP Out | Sends one datagram from the connection's bound socket. |
| WebSocket In | Starts a run for each native text or binary message. |
| WebSocket Out | Writes one native message to a client peer, an incoming session, or all listener sessions. |

These nodes run on the server and can reach LAN endpoints. A successful write means
the transport accepted the bytes, not that a device acted on them. Actions have
`out` and `error` branches and never retry automatically, even with an authored
retry policy. A write or broadcast that fails may already have reached some peers.

## TCP settings and framing

Client mode connects to a remote `host:port` and reconnects after a disconnect.
Listener mode binds the configured address. **Request only** mode stores the endpoint
without opening an idle connection, for use with TCP Request. TCP In cannot use a
request-only connection, and TCP Request cannot use a listener.

The connection sets the framing for its shared input stream:

- `chunks`: each transport read is a message. Chunk boundaries are arbitrary.
- `delimiter`: escaped bytes such as `\n`, `\r\n`, or `\x00` end a frame.
  Include delimiter keeps those bytes in the payload. The byte limit counts the
  delimiter even when the payload omits it.
- `fixed`: frame length counts bytes, not characters.
- `close`: the peer's orderly end of stream ends one message.

Delimiter and fixed framing reject a partial frame at EOF. A frame over the byte
limit closes the peer. TCP Out can append escaped delimiter bytes and close the
session after sending. It does not queue sends while offline or replay them after a
reconnect.

TCP Request opens a fresh socket for every execution, so concurrent requests cannot
receive each other's responses. The response ends at a delimiter, a fixed length,
peer close, or an idle gap after the first response bytes. One overall timeout
covers connect, TLS handshake, send, and read, and an idle gap does not extend it.
Close write side tells the peer the request is complete while the response side
stays open. On success the node replaces the payload and socket metadata and keeps
the input topic, other metadata, and group stack.

## UDP settings

Select `udp4` or `udp6` and a bind address. The default is a loopback address with
an assigned port. Input and output share that socket. Set a default destination on
the connection or a `host:port` destination on UDP Out, where `{{ }}` expressions
can pick a peer from the message.

Reply sends to the incoming remote address, after checking that the message's
connection ID matches the selected connection, and keeps the bound source port. UDP
has no session identity or delivery acknowledgement. Every payload is one datagram.
An oversized datagram is dropped, never split, and adjacent datagrams are never
joined.

For multicast, set a group IP, an optional interface name, TTL / hop limit, and
loopback. The group must match the chosen address family. IPv4 broadcast requires
Allow IPv4 broadcast. IPv6 has multicast and no broadcast. To receive multicast on
an interface other than loopback, bind a wildcard address such as `0.0.0.0:9000`.

## WebSocket settings

Client mode uses a `ws://` or `wss://` URL, optional encrypted headers JSON, and
subprotocols. The client refuses redirects. Put credentials in the headers JSON,
not in the URL's user information. Listener mode binds a dedicated HTTP(S) port and
an exact path, `/ws` by default. It does not use Karakuri's application event
socket.

An optional listener bearer token requires `Authorization: Bearer <token>`. An
empty origin list applies same-origin checks. Add permitted browser origin host
patterns when another site needs access. Origin checks do not stop a client that
sends no Origin header, so set a bearer token when the listener needs
authentication. The listener negotiates subprotocols from the configured list. Its
session limit counts clients still in the HTTP or TLS handshake along with
connected sockets.

TLS takes inline PEM CA, certificate, and private key fields. Listener TLS needs
both a certificate and a key. Client TLS verifies the server against system roots
or the configured CA, and server name can override the name checked on the
certificate. The minimum is TLS 1.2. Karakuri stores private keys, client headers,
and listener tokens encrypted and shows them masked. Saving a masked value keeps
the stored secret.

## Payloads and replies

Input formats are Text, Binary, Base64, and JSON. Text returns a string, Binary
returns bytes, Base64 returns a string, and JSON can return an object, array, or
scalar. Invalid JSON logs an error and drops the frame. Remote JSON only becomes the
payload. It cannot set the message's ID, its split and join grouping, or where the
run continues.

Output formats use the same choices. Text requires a string, Binary requires bytes,
Base64 decodes a string, and JSON serializes the payload. On WebSocket, Binary and
Base64 send binary frames and Text and JSON send text frames. An input node's Test
payload seeds a test run without network traffic. Binary test payloads are base64
strings.

Incoming messages carry these metadata keys:

| Key | Value |
| --- | --- |
| `socket_connection_id` | Settings connection UUID. |
| `socket_session_id` | Transient TCP or WebSocket session UUID. Empty for UDP and private requests. |
| `socket_protocol` | `tcp`, `udp`, or `websocket`. |
| `remote_address` | Peer address, including port. |
| `local_address` | Bound address, including port. |
| `socket_frame_type` | `text` or `binary`. TCP and UDP deliver bytes. |

Keep these keys on the message to reply to the incoming session. Configured peer
sends to a client connection's peer. Incoming peer / session checks this connection
and the exact live session. All listener sessions broadcasts, and only when
selected. A stale or foreign session fails. It never selects another peer or falls
back to a broadcast.

## Bounds and lifecycle

Frames and responses default to 64 KiB, with a 1 MiB ceiling. A TCP Request
response limit above its connection's limit uses the connection's limit. The UDP
ceiling is 65507 bytes. Connections default to 16 sessions and allow up to 64.
Connect, write, and reconnect durations are positive and at most 120 seconds. TCP
Request defaults to a 30-second overall timeout and a 200 ms idle gap.

Each connection admits at most 64 subscriptions. Each subscription queues at most
32 frames and 4 MiB. A full queue drops incoming frames and logs an error, so a slow
flow never stops the connection from reading. The usual limits on concurrent runs
still apply. Replacing a connection discards its pending frames and retires every
session ID. Canceling a run interrupts its writes and closes its TCP Request socket.
Sockets, subscriptions, and queues do not survive a server restart.
