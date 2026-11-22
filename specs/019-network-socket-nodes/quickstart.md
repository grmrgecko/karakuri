# Validation: Network socket nodes

1. Build with `make dev` and run server/frontend Makefile tests.
2. Restart karakuri-dev.service and verify active status and localhost:8080.
3. Create disposable localhost TCP, UDP and WebSocket connections through MCP.
   Inspect all seven node metadata entries and exercise isolated sends/requests.
4. Deploy disposable input flows. Send fragmented TCP frames, empty UDP packets
   and WebSocket messages; inspect peer metadata and reply through the same socket.
5. Replace or disable connections and verify input subscriptions resume or stop,
   old session IDs fail and listener ports are released. Use local peers only.
6. Inspect all nodes in the browser; change formats and response modes, capture
   after interactions, verify live port typing/show-if controls and console.
   Keep visual edits unsaved by pausing draft writes.
7. Remove disposable connections/flows. Record evidence here and leave the
   development unit running. Do not alter household devices or existing secrets.

## Evidence, 2026-10-06

- `make dev`, `make test TARGET=server` and `make test TARGET=frontend` passed.
- `GOFLAGS=-race make test-integration` passed against the isolated HTTP suite
  and `test/sockets` real peers. Checks cover TCP framing, response completion,
  partial/oversized/late responses, concurrent private requests, cancellation,
  TLS verification, strict replies, listener rebind, UDP IPv4/IPv6 datagrams,
  multicast TTL and broadcast permission, WebSocket handshake policy and cleanup.
- Native vet and socket-package Windows/macOS vet passed.
- MCP resolved all seven nodes in text/binary/base64/JSON formats. Deployed
  local TCP, UDP and WebSocket echo flows received messages and replied through
  the incoming peer. TCP and WebSocket inputs resumed after settings replacement;
  retired TCP sessions failed. A masked WebSocket bearer survived replacement.
  Standalone TCP Request and UDP binary-port conversion passed through MCP.
- Browser checks rendered all seven nodes. TCP input text/binary/JSON, TCP
  Request binary input/output, UDP output binary, TCP fixed framing and request
  fixed/idle controls updated live. Connection forms exposed TCP/UDP/WebSocket
  settings and WebSocket listener fields. Screenshots followed interactions;
  final console and runtime error capture were empty.
- Visual changes stayed unsaved. Disposable socket flows and connections were
  deleted, including their deployed versions. Existing household devices,
  secrets and older test flows were left alone.
- The HTTP integration harness now binds its agent listener to loopback port 0
  so it can run alongside the required development service on port 9443.

Convergence reviewed 11 requirements, five success criteria, 14 acceptance
scenarios and the five constitution principles. The remaining HTTP-upgrade
handler lifetime gap was completed as T011; the repeated race-enabled
integration suite passed. No implementation gaps remain.
