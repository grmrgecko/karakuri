# Verification: Desktop interaction and vision

Run make dev TARGET=agent, make dev TARGET=auto, make test TARGET=agent, and make test TARGET=automation. Build Windows, macOS, and FreeBSD amd64 through the same dev targets. Build/test server for embedded docs and frontend for editor checks, then restart karakuri-dev and verify active/HTTP 200.

Use disposable windows on documented hosts. Record original geometry, place/resize/restore, and compare newly listed actual bounds. Drag a fixture widget, inspect its final label, cancel a timed drag, and verify a later click works without a held button. Use current desktops, recording explicit unsupported paths instead of changing user sessions unnecessarily.

Crop a distinctive image and match it at the same scale, including repeated targets and a no-match case. Large expensive scans must fail with a work-limit error. Retain captures/results outside tracked source.

Use MCP to check node advertisement, optional binary image/reference inputs, capture fallback, and observed output. In the collaborative preview inspect the new fields and ports, take settled screenshots after interactions, check the console, and remove unsaved verification nodes. Do not add saved test flows.

Deploy committed binaries to the seven active enrolled agents and refresh canonical CLIs, preserving Mac signing/grants. Verify reconnection, versions, new schemas, and applicable executions. Close fixture windows and leave the development server running.

## Checks completed on 2026-10-06

Agent, automation, and server Makefile tests passed. Linux, Windows, macOS, and
FreeBSD amd64 agent/CLI builds succeeded. Linux, Windows, and FreeBSD vets passed.
The Mac module vet still reports existing unsafe.Pointer diagnostics in ffi.go:196
and workspace.go:81; neither file changed. Mac vet with that analyzer disabled
checks the remaining analyzers separately.

All seven agents advertised 53 node types and executed binary image matching
through MCP. A captured 520x360 source located its 70x55 reference at (50,90) with
100 percent agreement on every host. Required binary value fields are supplied
as base64 configuration literals for standalone tests; normal flow graphs still
require their value wires. The MCP fixture verifies valid binary data and missing
required bytes without adding saved flows or history rows.

| Desktop | Placement and drag | Fresh capture matching |
| --- | --- | --- |
| Arch Xfce/X11 | 520x360 at (120,100) observed exactly; GTK block moved and released | Cropped reference found at 100% |
| Rocky GNOME/X11 | 520x360 at (120,100) observed exactly; GTK block moved and released | Cropped reference found at 100% |
| FreeBSD KDE/X11 | Konsole placement observed exactly; titlebar drag moved it by (60,40) | Cropped reference found at 100% |
| macOS | TextEdit placement observed exactly; titlebar drag moved it by (60,40) | Cropped reference found at 100% |
| Local Hyprland/Wayland (user and root agents) | Floating GTK placement observed exactly at (120,100), 520x360; both block drags moved and released; tiled placement rejected | Cropped reference found at 100% |
| Windows 11 | WinForms placement observed exactly; block drag delivered after activating the disposable window through the resident | Cropped reference found at 100% |

The initial Windows agent pointer calls were refused while another application
had focus. Activating the disposable fixture through the existing resident allowed
the drag check; the agent task remains Limited. No privilege, identity, or privacy
grant was changed. Direct pointer permission failures remain explicit errors.

CLI file matching passed exact-match, completed no-match, and transparent-reference
rejection checks. Algorithm fixtures cover fractional score arithmetic, repeated
targets, output truncation, nonzero image origins, truncated PNG, and comparison
budget errors. Two real authenticated clients exercise drag disconnect cleanup
and prove that queued input waits for button release.

The collaborative editor exposed both binary sockets as search/reference settings
changed, accepted 97.5 percent, and showed conditional window selectors for drag.
Placement advertised object output and restore/bounds settings. Screenshots followed
interactions and the console was empty. Three unsaved verification nodes were
removed; the existing 34-node draft was preserved. No saved test flow was created.

Captures, fixture state, and MCP results are retained outside source under
/tmp/karakuri-interaction-verification and the test users' home directories.
KWin Wayland and Sway placement have platform builds and source verification,
but were not exercised on those compositors during this check.

Hyprland 0.56.2 uses Lua dispatchers. Placement selects the API from the reported
compositor version before acting, resizes before moving, and reports fresh bounds.
Live drag checks exposed interrupted compositor reads; read-only receives now
resume on EINTR without replaying input requests. The final Windows mouse code
was verified again: its fixture moved to (357,242), recorded a second drop, and
reported no held button.

All six disposable verification windows were closed through exact window IDs and
subsequent window lists confirmed their removal. Existing user windows and the
unsaved flow draft were preserved. The development service is active and answers
HTTP 200. Frontend validation reports zero errors and warnings.

## Final rollout

Commit 6304a87 was built for Linux, Windows 11, Intel macOS, and FreeBSD amd64.
All seven agents reconnected with 53 node types, including the three new tools.
Their final installed builds executed the exact binary-image fixture through MCP,
and all seven captured fresh screenshots successfully. The five canonical desktop
CLIs reported 6304a87 and passed headless file matching at 100 percent.

Mac uploads reused both existing signing identities and privacy grants. Launchd
briefly rejected bootstrap immediately after bootout; a subsequent bootstrap of
the same existing plist succeeded. The agent and resident reconnected, and fresh
capture succeeded after the final signing/update. No enrollment or grants were
regenerated. Windows 7 remained excluded.

Final versions and results are retained under /tmp/karakuri-interaction-verification.
All fixture windows remain closed, and the existing development server remains
active with HTTP 200. No saved test flows were added.
