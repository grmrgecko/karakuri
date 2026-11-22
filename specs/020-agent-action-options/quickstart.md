# Validation: Agent action options

Use make dev and make test with agent/server/frontend/automation targets as applicable.
Restart karakuri-dev.service after builds and verify localhost:8080 and active state.

With an existing enrolled agent, use MCP test_node for:

1. Command/script exit 1 accepted by [0,1], exit 2 rejected, payload retained through
   meta.command_result; file-only stdout above 32 MiB succeeds and return mode fails.
2. Local HTTP peer GET/HEAD, expected statuses/body, disabled redirects, POST JSON,
   binary output, size limits, TLS refusal/opt-in and request cancellation.
3. Disposable user service start/stop and enable/disable confirmation. Remove it afterward.
4. Temporary file create-only collision, permissions and matching/mismatching SHA-256.
5. Desktop region capture with a small reference: compare mapped match bounds against
   reported desktop rectangle. Binary source must report mapping unavailable.

Browser: create an unsaved draft, change command result destination/stdout mode and HTTP
output format, capture settled screenshots after each interaction, verify port types and
inspect console. Report any saved verification flows. Live desktops use canonical
~/karakuri-auto paths and disposable windows per AGENTS.md.

## Completed checks, 2026-10-06

- Makefile builds succeeded for server, frontend, agent and automation CLI. Agent/CLI
  cross-builds succeeded for Windows, Darwin and FreeBSD amd64.
- Agent, server, frontend and automation test targets passed. Frontend reported zero
  errors and warnings. Real-process fixtures cover accepted/rejected exits, launch
  failure, cancellation, nested result preservation and 32 MiB + 1 byte output.
  File fixtures cover concurrent exclusive creation, permission reset/preservation,
  hash mismatch and untouched symlinks. Local HTTP/TLS peers cover JSON and decoded-binary POST, redirects,
  origin header stripping, formats/overflow, trust bypass and timeout/cancellation.
- MCP on localhost confirmed command/script accepted exit 1 with preserved payload
  and metadata result, HTTP request JSON response, probe status/body health and a
  33,554,433-byte file-only command result. HEAD health returned 405 from the server,
  failed under default expected statuses, and returned null with explicit expected 405.
- MCP confirmed start, enable, disable and stop of a disposable systemd user service,
  preserving payload and returning confirmed observations. The unit was removed after
  stop/disable and daemon-reload. File create-only collision and mismatched digest
  failed; matching digest replaced content with mode 0644. Temporary files were removed.
- MCP image matching confirmed exact region/reference matches with known origin/scale,
  desktop bounds and center on Rocky Linux X11, macOS, Windows 11 and FreeBSD X11.
  Rocky used a disposable GTK window; it was closed. Region origin 189,151 mapped
  the 70x55 match center to 224,178.5. Other hosts used origin 100,100 and center
  135,127.5. These displays used scale 1; independent/negative/scaled geometry is
  covered by helper PNG and mapping fixtures. Binary input reported mapping_known
  false and omitted desktop bounds/center.
- Updated agent/CLI binaries on those live hosts using staged canonical paths.
  Mac binaries retained their existing stable signatures and grants. Existing
  enrollment identities, database contents and encryption material were preserved.
- Browser DOM interactions in the existing Agent test localhost draft verified
  command text output, property destination with preserved Object payload, discarded
  stdout with no returned format assertion, and HTTP response Binary/Object changes.
  Console error/unhandled-rejection capture remained empty. Changes were left unsaved;
  no verification flows or saved runs were added.
- The collaborative preview snapshot tool returned PreviewAutomationExecutionError
  after every attempt, including after resize/reopen and alternate snapshot options.
  Screenshots are unavailable; browser evidence is DOM interaction and console checks.
- Development service was restarted, active, and localhost:8080 returned HTTP 200.

Service confirmation live checks covered systemd. macOS/Windows confirmation code
was cross-built; native manager confirmation was not exercised on those hosts.
Portal/native-window captures without reliable mapping remain explicitly unknown.
