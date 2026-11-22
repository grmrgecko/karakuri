# Tasks: Network socket nodes

- [x] T001 Read repository constraints and produce the specification, design, contracts and checklist in `specs/019-network-socket-nodes/`.
- [x] T002 Implement bounded subscriptions, configuration validation, TLS and session lifecycle in `pkg/integrations/socket/`.
- [x] T003 Implement TCP framing, shared connections and isolated requests in `pkg/integrations/socket/tcp.go` with real peer checks.
- [x] T004 Implement UDP sockets, destinations and multicast in `pkg/integrations/socket/udp.go` with real peer checks.
- [x] T005 Implement WebSocket clients and dedicated listeners in `pkg/integrations/socket/websocket.go` with real peer checks.
- [x] T006 Implement and register seven nodes in `pkg/nodes/core/sockets.go` and connection kinds in `pkg/nodes/core/plugin.go`.
- [x] T007 Verify cancellation, stale replies, bounds and replacement with real protocol integration tests.
- [x] T008 Update `docs/network-sockets.md`, feature indexes and baseline specifications.
- [x] T009 Build and test through Make, restart the development unit and exercise MCP and editor controls.
- [x] T010 Record validation evidence, remove disposable fixtures and commit the verified change.

## Phase 2: Convergence

- [x] T011 Track HTTP upgrade handler lifetime in `pkg/integrations/socket/websocket.go` so Stop waits for handshakes and established reads per FR-009 and the plan's owned-goroutine shutdown contract (partial).
