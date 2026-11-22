# Validation guide

Build and test with `make dev TARGET=agent`, `make dev TARGET=auto`, `make test TARGET=agent`, and `make test TARGET=automation`. Build the server after guide edits, restart `karakuri-dev.service`, and confirm HTTP 200 on port 8080.

Use a disposable application on the AGENTS.md hosts. Run UI Snapshot, Click Control, Wait for UI State, and Screenshot Now in sequence. Check duplicate target rejection, protected text, window disappearance, timeout evidence, and a fresh capture after an action. Inspect menus and dialogs, then exercise a harmless menu item. Read display modes and verify rejection of an unsupported mode or restore the original after a temporary change.

Use the MCP catalog and test_node endpoints for advertised nodes. Inspect their editor fields and ports in the browser, taking settled screenshots after interactions and checking the console. Leave visual-only drafts unsaved.

Run a command file with a known successful command, a failing command, and a final command whose output must be absent. Confirm the Automation Script node surfaces the first failure and bounded stdout/stderr.

## Verification on 2026-10-06

- Agent, automation, and server test targets passed. Linux amd64 agent/CLI and server builds passed. Agent and CLI builds passed for Windows, macOS, and FreeBSD amd64. The patched Windows 7 toolchain also built the agent and CLI. Its VM was later removed by the operator and is no longer a test target.
- Arch Linux/Xfce X11 passed all twelve additions through MCP. Checks covered protected text, duplicate labels and window matches, current window metadata, selected-window captures, accessible and pixel-stability waits, timeout evidence, menu invocation, dialog discovery, and unsupported display-mode rejection. Automation Script stopped at its first failing line; the final screenshot was absent.
- A seven-node flow read snapshots and screenshots before and after a control click in one run. The label changed from Ready to Completed, timestamps differed, and PNG output changed from 12,249 to 12,504 bytes. Run `7fc5817a-e44d-45c0-b42e-7c9df69ad23d` succeeded. The temporary flow was deleted after verification.
- Windows 11 passed agent snapshots, accessible waits, native label clicks, disappearance waits, dialog discovery, and captures. UIA title-bar geometry failures now retain the rest of the snapshot and report `geometry_error`. An application launched by the elevated CLI resident rejected actions from the ordinary agent; launching the fixture through the agent passed. Windows integrity boundaries remain in force.
- macOS passed snapshot, native capture, and control click through the launchd helper. This exposed and fixed missing Desktop initialization in ServeHelper. The canonical executable retained its development signing identity. FreeBSD/KDE X11 passed snapshot, capture, dialog discovery, and accessible state waits; its Qt provider omitted some labels.
- The editor displayed the new execution input, binary image output, and error port. Switching capture modes changed the window-selector and region fields live. No new browser console errors appeared. The visual-only draft was discarded without saving.
- Disposable application windows were closed. Captures and timeout evidence remain in the test users' home directories. Existing agent identities, credentials, databases, and unrelated application windows were preserved.

These checks do not establish provider support for every application, multi-monitor placement, or every Wayland compositor. Display changes were checked using unsupported-mode rejection; a successful resolution change was not exercised.

## Agent deployment verification

All seven reachable agent instances ran commit `d0ef63c` after deployment: Arch,
Rocky 9, FreeBSD, macOS, Windows 11, localhost, and localhost-root. MCP reported
50 advertised nodes per instance. A Command node on each returned that build ID.
Existing identities and configuration directories were retained.

Window/display lists, screenshots, and dialog discovery passed on all five remote
hosts. Disposable application tests covered snapshots, control clicks, waits, and
selected-window screenshots. Rocky 9 required a desktop login as grmrgecko;
lingering was enabled so the user service survives SSH logout. Its older GTK
provider exposed the GetActions crash fixed in `d0ef63c`. Arch and FreeBSD action
checks were repeated with the fix. macOS passed after the operator enabled the
installed agent's Accessibility and Screen Recording grants; both grants survived
a subsequent signed agent update using the original certificate.

Both local agents passed command execution and desktop metadata checks. After
the operator approved Wayland portal capture, Screenshot Now passed on both the
user and root agents, returning valid 1720 x 1304 PNGs. This resolved the earlier
portal timeouts. The operator removed the Windows 7 VM and withdrew that test
requirement; it is no longer an outstanding deployment target.

No test flow was added during deployment. Disposable test windows were closed;
JSON results and captures were kept outside tracked source. The development
service remained active and returned HTTP 200 after the updates.

## Configuration improvement checks

Constrain similar windows by title, class, PID, and image in each match mode.
Type into a selected disposable application with activation and verify input is
blocked when another window has focus and activation is off. Exercise disabled
state, fractional timeouts, single checks, and normal unmatched outcomes; provider
failures and ambiguity must still fail. Read both snapshot sources with filters
and a limit, scope dialog discovery, and capture desktop-coordinate regions.
Narrow duplicate controls by role or immediate parent. Run inline command text,
including a failing line, and confirm its temporary file is removed. Check the
editor's conditional fields and console. Close all disposable applications.

### Configuration verification on 2026-10-06

Arch/Xfce X11 passed process/title/class/executable filtering, literal matching,
single checks, disabled-state predicates, fractional timeout results, ambiguity
rejection, protected-value filtering, both snapshot sources, per-source limits,
keyboard activation and input observation, unfocused-input rejection, parent/role
control constraints, scoped dialogs, and desktop-coordinate region capture.
The short timeout check caught a desktop read crossing the deadline; waits now
retain the last valid unmet observation while excluding joined provider errors.

Windows 11 passed both snapshot sources and truncation, keyboard activation,
scoped dialogs, selected-window capture, and clicking a native HWND button with
its parent HWND and class constraint. macOS passed the same observations through
its helper, with a role/parent-constrained label click. Both disposable dialogs
closed and disappearance checks passed. Full Windows desktop capture exceeded
test_node's response limit and returned a truncated result; its selected-window
capture returned image output.

Local Automation Script checks used a real executable to read its generated file.
Literal template text was preserved, and the temporary file was absent after
success and nonzero exit. The browser switched file/inline fields, displayed the
new wait options, accepted 0.25 with valid number-input state, and added a Number
input socket live when Window Process ID was exposed. Screenshots were taken
through the collaborative preview; its console reported no errors. Visual edits
were left unsaved and the added verification nodes were removed. No test flow
was added to the development database.

Agent, automation, server, and frontend Makefile checks passed. Builds passed for
Linux, Windows, macOS, and FreeBSD amd64. Live results are under
/tmp/karakuri-improvement-verification, outside tracked source.

### Final configuration deployment on 2026-10-06

Build `8a300ed` was deployed to Arch, Rocky 9, FreeBSD, macOS, Windows 11,
localhost, and localhost-root. Each reconnected with 50 advertised nodes and
the new configuration fields, including the original `enabled` option. Command
execution confirmed the committed build on every host. Canonical CLI binaries
were replaced through staged files and their resident installations refreshed.
All seven agents passed literal inline CLI commands, single missing-window
checks, 1.25-second unmatched waits, empty scoped dialog queries, window/display
lists, and valid desktop-coordinate region PNG captures.

The full Arch interaction suite passed again on the committed build. Rocky's
older GTK provider passed both snapshot sources and limits, legacy enabled
checks, disabled-state checks, targeted typing with observed text, and a
parent/role-constrained click with observed state. Dialog discovery passed after
waiting for the new window to appear. FreeBSD passed both snapshot sources and
limits, targeted keyboard activation, scoped discovery without controls,
window capture, and a parent/role-constrained element-ID click. macOS repeated
its snapshot, focus, scoped dialog, capture, and constrained label-click checks.

The Mac agent retained its signing certificate and permissions. launchd rejected
the first restart with `EX_CONFIG`; reloading the existing LaunchAgent plist
restored the service without changing enrollment or signing identity. Windows
passed scoped dialogs, capture, and native HWND clicking on the committed build.
Its final keyboard retest encountered Windows foreground-activation refusal.
The node returned that error without typing; an earlier activation check passed.
Full Windows desktop capture was also verified through the canonical CLI to
avoid the MCP response limit.

All disposable windows were closed. No test flow was added, and existing agent
identities, configuration, and encrypted secrets were retained. Results and
captures remain under /tmp/karakuri-improvement-verification. The development
service was restarted and checked for an active unit and HTTP 200 response.
