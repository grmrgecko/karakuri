# Validation: Graceful process termination

1. Run make dev TARGET=agent and make test TARGET=agent. Real process fixtures cover cooperative cleanup, escalation, descendants, normal completion, and invalid config.
2. Cross-build with make dev TARGET=agent GOOS=windows GOARCH=amd64, and corresponding darwin/freebsd targets. Windows live fixtures must verify zero grace works and positive grace refuses launch.
3. Run make dev TARGET=server and make test TARGET=server. Restart with systemctl --user restart karakuri-dev.service, verify active status and an HTTP 200 from localhost:8080.
4. Against an updated enrolled development agent, inspect command/script metadata through MCP. Test a disposable shell program with a TERM handler, timeout 1 second, and termination_grace 500ms. The handler writes a final marker; the node still fails for timeout. Repeat with an ignoring program and verify it stops producing output.
5. In the editor, open an unsaved draft, add each node, and inspect/set Termination grace. Capture settled screenshots and check the console. Save no visual-test draft.

## Verification evidence (2026-10-06)

- Agent and server Makefile builds/tests passed. Agent cross-builds passed for Windows amd64, macOS amd64, and FreeBSD amd64.
- Real POSIX fixtures passed for command/script cooperative cancellation and timeout, immediate default, ignoring processes, normal completion, duration bounds, and descendants after leader exit with return/file/discard streams. Deadline fixtures verified grace allowance and unchanged unrelated nodes.
- Live MCP on the existing localhost agent verified command/script TERM handlers completed a delayed cleanup-marker write after a one-second node timeout with 500ms grace. Both nodes remained failed, with one attempt. Markers were removed.
- Live MCP with an ignoring command, one-second timeout, and eleven-second grace returned the node timeout after 12.004 seconds. The outer wrapper did not cut cleanup off at its former eleven-second deadline.
- Updated the existing Windows 11 agent executable/task without changing its identity. Live MCP verified command/script zero-grace execution and positive-grace rejection. Both pre-existing output files retained their original contents; fixtures were removed.
- Collaborative browser DOM inspection confirmed the Termination grace field and help text on both existing localhost command/script nodes. Console/error capture recorded no errors. No graph edits, flows, or saved runs were added.
- The collaborative browser's snapshot and focused interaction tools repeatedly returned generic execution failures, including after navigation and a fresh tab. DOM inspection worked through preview_evaluate, but screenshots and a full visual interaction check remain unverified. Existing test drafts were left unchanged.
- macOS and FreeBSD process behavior was cross-built but not exercised live. The Windows-specific test function was not run locally; its behavior was checked through the actual Windows agent instead.

The development service was restarted and verified active with HTTP 200 from localhost:8080. Existing databases, identities, and encrypted configuration were preserved.
