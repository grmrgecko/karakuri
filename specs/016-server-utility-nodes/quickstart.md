# Verification: Server utility nodes

1. Run `make dev TARGET=server` and `make test TARGET=server`.
2. Restart `karakuri-dev.service`; verify it is active and localhost:8080 responds.
3. Use MCP `get_node_type` for all three types and `test_node` with scalar/nested
   numbers, a timestamped overnight window and an existing connection status.
   These checks must not command devices or save test flows.
4. Create a temporary disabled flow in the browser. The editor autosaves drafts;
   delete the flow and any status snapshot test run after verification. Place all
   nodes, change Numeric transform
   paths/operation, and verify derived ports and conditional fields. Switch window
   endpoints between clock and sun. Select connection/initial emission. Capture
   settled screenshots after interactions and inspect console diagnostics.
5. Record actual evidence below after verification.

## Evidence

Verified on 2026-10-06.

- `make dev TARGET=server` and `make test TARGET=server` passed, including the
  Home Assistant Python suite. The real HA WebSocket fixture exercised failure,
  recovery, disablement, replacement, cancellation and retained startup errors.
- Vet and race checks passed through temporary Makefile targets:

  ```sh
  make --eval=$'vet-server:\n\tgo vet ./cmd/karakuri ./internal/... ./pkg/... ./plugins/...' vet-server
  make --eval=$'race-nodes:\n\tgo test -race ./pkg/connmgr ./pkg/nodes/core' race-nodes
  ```

- MCP catalog reads found all three types. Standalone execution passed nested
  scale writes with preserved metadata, reversed/clamped mapping, negative
  rounding, energy conversion, Friday overnight routing, exclusive end and
  polar-night metadata. Invalid maps, units and readings failed without output.
  Celsius zero converted to exactly 32 Fahrenheit. Current Home Assistant,
  AI and MCP connection snapshots returned their connected statuses.
- Strict graph validation accepted an object status wired into a nested numeric
  source, followed by Time window and Debug. Changing the numeric source to a
  scalar rejected that object-to-number edge.
- Browser edits changed Numeric transform input/output labels from Number to
  Object for nested paths. Convert showed unit fields and hid factor/offset.
  Time window showed independent event/offset fields after switching both
  boundaries to sun. The connection selector included every integration kind;
  selecting an MCP connection and enabling initial emission persisted in the
  draft. Screenshots were captured after interactions and inspected after settling.
- Browser diagnostics contained only the existing latest-run endpoint 404s for
  flows without run history. No JavaScript exception or new node diagnostic appeared.
- The temporary flow `ae3f653e-5b27-43a2-aa21-c26794f586ce` contained three nodes
  and one status snapshot test run. It was never deployed or enabled, and was
  deleted after checking. No temporary flow remains and no device was commanded.
- After restarting, `karakuri-dev.service` was active and `http://localhost:8080/`
  returned HTTP 200. Existing connection credentials remained readable.

Selected local browser artifacts under
`/home/grmrgecko/.t3/userdata/browser-artifacts/`:

| Check | Screenshot |
| --- | --- |
| Nested numeric Object ports | `browser-screenshot-10-0-10-32-mux7i0ie-d8db0854.png` |
| Unit conversion fields | `browser-screenshot-10-0-10-32-mux7jh65-c65236d8.png` |
| Both solar boundaries | `browser-screenshot-10-0-10-32-mux7l5jf-0d6116aa.png` |
| MCP connection and initial emission | `browser-screenshot-10-0-10-32-mux7ngc4-4f4cc8de.png` |

Screenshots are local verification artifacts and are not committed.
