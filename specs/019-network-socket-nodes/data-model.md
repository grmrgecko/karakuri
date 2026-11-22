# Data model: Network socket nodes

- Connection config: existing encrypted `models.Connection.Config`; kinds tcp,
  udp and websocket. Protocol-specific fields are validated before driver creation.
- Driver generation: immutable config, context, listener/socket, status, sessions,
  subscriptions and owned goroutines. Retired generations cannot accept new work.
- Session: random UUID, remote/local addresses, net.Conn or WebSocket, write gate.
  A reconnect always allocates another ID. Session identity is connection-scoped.
- Frame: immutable bounded bytes, protocol/frame kind, session ID and addresses.
  Subscribers share immutable received bytes; node binary payloads get their own copy.
- Subscription: bounded queue and queued-byte count; remove atomically on disarm,
  close and discard on stop. A slow subscriber never blocks socket readers.
- Request: private socket, context/absolute deadline and bounded response reader.
  Closing on cancellation unblocks I/O; no state survives the execution.

Message metadata names `socket_connection_id`, `socket_session_id`,
`socket_protocol`, `remote_address`, `local_address` and `socket_frame_type`.
UDP replies use the incoming remote address after matching the connection ID.
No live resources or credentials cross the message/history boundary.
