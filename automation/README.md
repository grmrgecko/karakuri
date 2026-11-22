# automation

Cross-platform OS automation for Go, without cgo. It drives a host's keyboard,
mouse, screen, windows, clipboard, and desktop session. Karakuri's agent nodes are
built on it, and it works on its own in any Go program.

```go
import "github.com/grmrgecko/karakuri/automation"

sess, err := automation.New(ctx, automation.Options{})
if err != nil { /* ... */ }
defer sess.Close()

sess.Keyboard.TypeText(ctx, automation.RealClock{}, "hello", 0)
wins, _ := sess.Window.List(ctx)
text, _ := sess.Clipboard.Read(ctx, automation.SelectionClipboard)
```

`New` probes the host and returns a `Session` with every capability filled in for
that host. A capability the host cannot provide is a stub that returns an error,
never a nil field, so a caller never checks for nil. `Session.Describe()` reports
the detected environment and the backend or helper transport chosen for each
capability.

The module's requirements and design plans are indexed in
[automation/specs](specs/README.md), split by public contract. CLI command
behavior is specified separately in
[specs/010-desktop-automation](../specs/010-desktop-automation/spec.md).

## Platforms

| Platform | Input | Screen | Windows | Clipboard |
|----------|-------|--------|---------|-----------|
| Linux X11 | XTEST | X server | EWMH | xclip/xsel/wl-clipboard |
| Linux/FreeBSD Wayland (KDE/KWin) | RemoteDesktop portal¹ | Screenshot portal | KWin scripting | klipper |
| macOS | CoreGraphics events² | CGDisplayCreateImage | Accessibility | NSPasteboard |
| Windows | SendInput | GDI | user32 | Win32 |
| FreeBSD X11 | XTEST | X server | EWMH | xclip/xsel |

¹ The portal opens a one-time consent dialog, brought up lazily on first use.
Pass a `TokenStore` in `Options` to persist its restore token and skip the prompt
next time. The portal also requests monitor sharing for absolute pointer moves.
Coordinates use the compositor's logical desktop space, including negative monitor
origins. A move requires a selected monitor with reported position and size;
missing geometry or a point outside the selected monitors returns an error.
Reading the current pointer position remains unavailable through this backend.

² Synthetic input and window actions need the process trusted for
Accessibility (System Settings > Privacy & Security > Accessibility); the first
call that needs it asks for the grant. Screen capture needs Screen Recording
permission, which the first capture asks for the same way.

The `Session` also carries cross-platform desktop services: notifications, native
dialogs through ncruces/zenity, speech, media control, idle time, power, and opening
URLs and apps.

## Delegation

A privileged process cannot reach a desktop session it does not belong to. The
Linux session bus answers only its owner. On macOS, keys a root process synthesizes
reach no session at all. On Windows, a service runs in session 0, which has no
desktop and cannot open another session's. When a privileged caller names a helper
command in `Options.HelperCommand`, `New` finds the desktop session a person is at
and returns a `Session` whose desktop capabilities run in a helper owned by that
user: this same binary, re-executed with the argv the caller named. `Sessions`
stays in the parent, where the privilege is needed. On macOS, the unlock's typing
and the session-context verbs, lock and fast user switching, also stay in the
parent, which drives them through the helper's keyboard and actor.

On Windows the same split holds cleanly: a SYSTEM caller keeps the Terminal
Services verbs and the credentialed unlock in the parent, and hands the desktop
I/O to a helper started as the console user, on the interactive desktop, through
`CreateProcessAsUser` with the user's token from `WTSQueryUserToken`. The
desktop is named in the child's startup information, so every thread the helper
later runs answers input on the right desktop. Only LocalSystem may mint that
token, so this path is the system-service agent's; an agent that already runs in
the session, or the CLI's resident, reaches the desktop without it.

A caller that already owns the session but was started outside it, over SSH, from
cron, or from a service, needs no privilege, only the session's environment. When such a caller names `HelperCommand` and holds no live display, `New`
discovers the user's own graphical session by reading the display, authority,
runtime, and bus variables out of one of their running processes. On Linux and
FreeBSD it hands those to a helper started as the same user, so a person's own
automation over SSH reaches their desktop with no installation and no resident;
FreeBSD's root delegate finds the session the same way, since it has no logind to
name one. On macOS the same detached caller delegates to its graphical session
through a temporary launchd job running the same executable. The helper starts on
the first desktop call and is removed when the session closes. The CLI and agent
both register helper commands, so they share this behavior. On macOS the
executable still needs Accessibility and Screen Recording permission and a stable
signing identity across updates.

Delegation requires `Options.HelperCommand`; zero-value options compose
locally. This differs from the CLI, which first adopts its own desktop environment
for same-user Linux and FreeBSD commands. `desktopenv.Discover` returns environment
data without changing the process environment, so a long-lived library caller
can pass it to a helper without rewriting variables under other goroutines.

A delegated helper call retries once when its peer disconnects and the caller's
context is still active. The helper can restart in the current graphical session,
but this does not guarantee that an action runs only once. Canceling the caller's
wait does not send per-call cancellation to that helper. Resident connections
created with `NewRemoteSession` instead close on cancellation and never replay a
lost reply. The owner must authenticate connections accepted by `SessionServer`;
the module does not create or secure the listener itself.

A live display in the caller's own environment means it is already in the session
and composes there directly, so a discovered session is only ever the user's own,
never one the caller could not otherwise reach. A stale or forwarded `DISPLAY`
does not count as live: the session is confirmed by connecting to its socket, so
an `ssh -X` forward or a value left over from an ended session is treated as
detached and the real desktop is found instead.

The session is found again each time a helper is started, not once at `New`. A
service that starts at boot, before anyone has logged in, still delegates: its
desktop calls report that no user is signed in until one is, and the first call
after that finds the session. A helper that restarts after the user logged out and
back in joins the new session rather than the one whose socket names and X
authority were captured at startup. The only capability that stays with the
process in the meantime is `Sessions`, which never needed a desktop.

## Desktop operations

`Session.Desktop` exposes native controls, menu trees/invocation, label clicks,
dialog discovery, and native window capture with visible desktop fallback. These
operations run inside the desktop helper when the caller delegates. They are also
available through `NewRemoteSession`. Native menu IDs are returned as strings;
dialog results contain lower-case window metadata and an inspection-error map.
On Windows, `elements` uses the same provider runtime IDs and roles as snapshots,
with a 4096-element limit.

`ReadWindowSnapshot` and `PollWindowState` share observations and state polling
between the CLI and agent. `SelectWindow` and `WaitWindow` provide explicit target
selection and presence waits for agent nodes. Validate `StateOptions` before
polling and supply a bounded context. `SaveStateEvidence` uses the caller's deadline;
it retains host-local evidence without hiding partial inspection failures.
`CaptureWindow` tries PrintWindow, XComposite, or CoreGraphics window capture
before falling back to desktop-coordinate screen capture. Protected accessibility
values are suppressed; pixels are not redacted.

Desktop control/menu actions and inspection invoke/display changes use one helper
attempt, and a lost reply is not replayed. Other helper methods retry once, as
described under [Delegation](#delegation). A shared session applies control and
menu actions one at a time along with other input.

Shared window selection accepts exact, contains, or regex matching and independent
title, class, PID, and executable constraints while retaining exact-ID precedence.
Single window/state checks distinguish unmet conditions from provider failure.
State checks support enabled, disabled, or either; snapshots can return filtered
accessibility elements and native controls with per-source output truncation.
`DialogOptions` narrows discovery before inspection and can omit controls; an
empty value finds every dialog. `ControlTarget` carries role, immediate-parent, and
label-match constraints through helper calls.

## Drag, placement, and images

`Desktop.Drag` accepts `DragRequest` with desktop start/end coordinates, button,
and duration from zero through thirty seconds. Local input adapters and helper
servers serialize it with keyboard, mouse, window, control, and menu mutations.
Release uses a fresh two-second cleanup context even after a failed press. Drag
and placement use one helper attempt. An ordinary helper can finish a bounded
drag after caller cancellation; shared-session disconnect cancels its server call.
Separate Sessions and agent processes are not serialized against each other.

`Desktop.PlaceWindow` accepts independent Restore, Move, and Resize flags in
`WindowPlacement`. `PlaceAndObserveWindow` re-lists the pinned window for up to
two seconds and separates requested bounds from observed bounds with a nullable
`BoundsMatched`. Unsupported providers fail explicitly. Native Windows, macOS,
X11/EWMH, and KWin request geometry from their window manager. Hyprland/Sway
require existing floating windows. Generic foreign-toplevel only restores.
Placement does not silently change a tiled window to floating. macOS can clear
fullscreen/zoom only when the application exposes those writable attributes.

`ReadImage` and `DecodeImage` load bounded PNG/JPEG images. `MatchImage` is a
capture-independent same-scale RGB comparison, with MinimumMatch in percent
and MaxResults. It returns image-pixel bounds, percentage scores, and truncation;
it never sends input. The score is `100 * (1 - mean absolute RGB difference / 255)`.
References must be opaque; source alpha is ignored. The default CLI/node minimum
is 98, while library callers supply a positive percentage through 100. MaxResults
zero uses ten, with a maximum of 100. Overlap merging uses IoU above 0.3. The scan
runs in row order and stops at the first additional distinct candidate beyond its
result cap, so truncation does not imply a globally best set of matches.

Encoded input is limited to 32 MiB, source images to 16 Mi pixels, references to
one Mi pixels, and scans to 100 million pixel comparisons. Work exhaustion and
cancellation return errors rather than a completed no-match observation. Use
smaller search regions when the work limit is exceeded. The functions have no
OCR, model, or external image-processing dependency.

The command syntax is under [karakuri-auto](#drag-placement-and-image-matching).

## karakuri-auto

`karakuri-auto` is the module as a command. `describe` reports the backend chosen
for every capability, which is the first thing to check on a host where a command
fails:

```
karakuri-auto describe
karakuri-auto version
karakuri-auto windows
karakuri-auto screenshot --region 100,80,640,480 section.png
karakuri-auto screenshot --window "Notepad" window.png
karakuri-auto screenshot --active-window active.png
karakuri-auto type "hello"
karakuri-auto unlock alice            # password on stdin
```

The command lives at `cmd/karakuri-auto` in the parent Karakuri repository and
ships as a separate binary archive. Over SSH it reaches the desktop the same way
the library does: as the session's own user on Linux and FreeBSD it adopts the
session's environment and drives the desktop in that one process, and as root it
delegates to the logged-in user's session. Neither needs a resident installed.
The macOS Accessibility grant follows the authorized executable and signing
identity. Preserve both across updates; an ad-hoc replacement can require new
privacy grants.

### Screenshots and windows

`windows` lists window IDs, titles, classes, desktop positions, and
sizes. A trailing `*` marks the foreground window. Use a listed ID to select one
window unambiguously:

```sh
karakuri-auto windows
karakuri-auto screenshot full.png
karakuri-auto screenshot --region 100,80,640,480 section.png
karakuri-auto screenshot --display 1 --region 0,0,320,240 second-screen.png
karakuri-auto screenshot --window 0x12345 window.png
karakuri-auto screenshot --window "Notepad" window.png
karakuri-auto screenshot --active-window active.png
```

The output defaults to `karakuri-auto.png`. Options can come before or after the
output path. `--region` takes x, y, width, and height in the selected screen's
coordinates, and screen indices start at zero. Width and height must be positive.
Put `--` before an output filename that starts with a dash.

`--window` accepts an exact ID or a case-sensitive substring of the ID, title, or
class. An exact ID wins, and multiple substring matches return an error.
`--active-window` selects the foreground window at capture time, which may be the
terminal that ran the command. Window capture needs usable geometry from the
desktop backend.

On Windows, X11, and macOS, window capture first asks for the window's own pixels,
which can capture a window covered by others without activating it. Some Windows
applications with unsupported or protected rendering still return blank content.
When the window's own pixels are unavailable, or take more than two seconds on
Windows, the capture falls back to the visible desktop rectangle, which includes
window borders and any overlapping windows. On Windows and X11 that rectangle can
span monitors and is clipped to desktop bounds. Wayland requires window geometry
from the compositor and captures the visible rectangle.

Choose one of `--region`, `--window`, or `--active-window`. `--display` applies to
full-screen and region captures. Window captures use desktop coordinates.

### Drag, placement, and image matching

The command and command files have the same operations as the library:

```sh
karakuri-auto drag 100 200 400 350 --duration 0.5 --button left
karakuri-auto window restore 'Karakuri verification'
karakuri-auto window place 'Karakuri verification' 120 100 640 480
karakuri-auto image-match capture.png reference.png --minimum-match 98 --max-results 10
```

CLI window actions require exactly one matching window. File-based `image-match`
does not need a graphical session.

### Desktop inspection

These commands work on Windows, macOS, Linux, and FreeBSD:

```sh
karakuri-auto controls "Setup"
karakuri-auto menu list "Notepad"
karakuri-auto menu invoke "Notepad" "File > Exit"
karakuri-auto click --window "Setup" --text "Next"
karakuri-auto elements "Setup"
karakuri-auto dialog list
karakuri-auto display
karakuri-auto display set 1280x800x32
```

Window selection follows the screenshot command's exact-ID or unique-substring
rules. `windows` also prints the PID, executable image, enabled state, and the
foreground `*` marker. Fields the desktop does not expose stay unknown. On Windows,
`controls` returns JSON with child handles, class, text, control IDs, rectangles,
visibility, and enabled state. The command skips a control that stops responding
after a short timeout instead of hanging. Password edit controls omit their text.
Enumeration stops at 4096 controls. `dialog list` returns a `dialogs` array of
`#32770` windows and their controls. Window records use the same lower-case fields
as snapshots, and inspection failures appear separately in `inspection_errors`.

`menu list` returns the native menu tree with item strings, paths, enabled and
checked states, and string IDs. An unreported checked state is `null`. `menu invoke`
accepts a numeric ID or a case-insensitive path separated by `>`. Paths omit
accelerator ampersands and shortcut suffixes. The command rejects disabled and
ambiguous items. Menus built only when opened, and custom menu widgets, may need
`elements` or keyboard navigation.

Label clicks prefer an exact label, then a unique substring, and ignore accelerator
ampersands. Standard buttons are pressed directly. Other controls are clicked at
their center, and only when that point belongs to the control. `elements` returns UI
Automation names, runtime IDs, roles, classes, state, and rectangles. It needs
Windows PowerShell 2.0 or later, which every supported Windows version includes, and
has a 30-second timeout and a 4096-element output limit. What custom UI exposes
depends on its provider. Elements whose provider rejects a geometry read have zero
dimensions and a `geometry_error`, and their IDs, roles, labels, and actions stay
available. `display set` tests the primary display mode before applying it. The
change is not saved as the default, and an unsupported mode returns an error.

On macOS, window listing includes normal windows and floating, modal, and utility
panels, including AppleScript dialogs. Mouse movement returns only after the pointer
reaches its destination, so an immediate click lands at the requested position.
`controls` and `elements` read the Accessibility tree on macOS and AT-SPI over the
desktop accessibility bus on Linux and FreeBSD. Both return element IDs, parent IDs,
names, text, roles, classes, rectangles, state, and available actions. Password
fields omit their values. Trees are capped at 4096 elements and 64 levels. IDs
belong to the application and can change when it rebuilds its UI. Menus expose
paths when the application's accessibility provider includes them, and
`menu invoke` accepts that path or a listed element ID. `dialog list` recognizes
accessibility dialog roles. Custom rendering still needs an accessibility provider
to expose text or actions.

On these platforms a label click prefers a measured control center in an activated
window and falls back to the provider's action when global coordinates are
unavailable. GTK providers can report window-relative coordinates on Wayland, and
Karakuri supplies global window bounds only when the compositor exposes them. On
GNOME Wayland, accessibility inspection works, but window crops and global control
coordinates are not guaranteed. Provider actions that open nested modal loops can
stall some GTK applications. A physical click avoids that when compositor geometry
is available.

Display management is built in on macOS and GNOME Wayland. Other desktops need
their utility installed: `xrandr` on X11, `hyprctl` on Hyprland, `swaymsg` on Sway,
`kscreen-doctor` on KDE Wayland, or `wlr-randr` on other wlroots compositors. Mode
changes target the primary output and are checked afterward. Unix desktops reject
color depth changes, so use the depth `display` reports. A compositor or driver can
reject a resolution that another desktop accepts.

### Launching, waiting, and state checks

Process launch, waits, and command files use the same syntax on every platform:

```sh
karakuri-auto run installer.exe /silent --wait
karakuri-auto wait window "Setup" --timeout 60
karakuri-auto wait window "Setup" --gone --timeout 60
karakuri-auto click 400 300
karakuri-auto sleep 0.5
karakuri-auto script steps.txt --log steps.log
```

`run` launches the executable directly, without shell expansion. It returns the PID
immediately unless `--wait` is the first or last argument. With `--wait`, it returns
stdout, stderr, and `exit_code=`, and the CLI exits with the application's code.
Waits poll windows every 100 ms for any matching ID, title, or class, and a timeout
is an error. Durations are in seconds and can be fractional. The outer request
deadline still applies to long waits and scripts.

State waits inspect an existing window without depending on title changes:

```sh
karakuri-auto snapshot "Setup"
karakuri-auto snapshot "Setup" --enabled
karakuri-auto snapshot "Setup" --all
karakuri-auto wait state --window "Setup" --text "Next" --enabled --timeout 60
karakuri-auto assert state --window "Setup" --text "Installation complete"
karakuri-auto wait state --window "Setup" --role dialog --gone
karakuri-auto wait state --window "Setup" --stable 1 --region 20,80,600,300
```

`snapshot` returns one JSON object with a UTC capture timestamp, window bounds and
ID, the backend names from `describe`, and accessibility elements. It uses UI
Automation on Windows, Accessibility on macOS, and AT-SPI on Linux and FreeBSD. By
default it includes only visible elements. `--enabled` also excludes disabled
elements, and `--all` includes hidden ones. The JSON shape is the same on every
platform. Element values use `text`, and protected values are omitted. An empty or
absent text value does not prove the UI has no text, because the provider may not
expose it. Roles and IDs keep their provider spelling, such as `button`,
`AXButton`, or `ControlType.Button`. Parent IDs can refer to filtered elements. An
unknown window enabled state is `null`, and `geometry_known: false` means the window
bounds cannot be used for cropping. Element coordinates are as the provider reports
them and can be window-relative on Wayland. `inspection_error` records a provider
failure, and the command then exits nonzero. A successful enumeration can still
omit custom-drawn content or optional fields the provider does not expose.

`wait state` and `assert state` behave identically and work in command files. They
select one existing window, reject ambiguous window matches, and keep waiting on that
window even if its title changes. `--text` matches a case-insensitive substring of an
element's name or unprotected text. `--role` matches the whole provider role,
case-insensitively. Conditions apply to the same visible element, and `--enabled`
requires it to be enabled. Multiple matching elements are an error, so narrow the
text or role. `--gone` succeeds when no visible element meets the conditions, or
when the selected window disappears, and it does not need to see the element first.
If accessibility fails during a `--gone` wait, polling continues until the window
disappears, inspection recovers, or the timeout expires. A failed read never counts
as an absent element, and a timeout includes the last inspection error.
Without `--gone`, a disappearing window fails the wait. These checks use accessibility
only and do not run OCR or read text from screenshots.

`--stable` waits for identical pixel samples over the given number of seconds and
cannot be combined with element conditions. An optional `--region` uses nonnegative
coordinates relative to the reported window bounds and must fit inside them. Without
a region, the command samples the whole window. Captures come from the visible
desktop, so occlusion, animation, and blinking carets affect stability. A stable
image does not prove that the application finished its work. The desktop must
support window geometry and screen capture. Samples run at most every 100 ms, and a
slow provider sets the actual rate. The default timeout is 30 seconds. A timeout
must be positive and longer than the requested stable interval.

Once a window has been selected, any failed state wait tries to save
`screenshot.png` and `snapshot.json` in a unique directory. The JSON records the
condition, the error, and an unfiltered element snapshot, including hidden and
disabled elements. Use `--evidence <directory>` to choose the parent directory. By
default, captures go under `karakuri-auto/captures` in the desktop user's
configuration directory (`~/.config`, `~/Library/Application Support`, or
`%AppData%`). They never go in `/tmp`, and nothing deletes them automatically. Each
capture has its own 10-second limit, so evidence collection can add up to 20 seconds
after a timeout. Capture failures are recorded in the JSON and reported without
replacing the state failure. Window metadata comes from the last successful
enumeration, so a capture taken after a window disappears can show the desktop at
its last location. A command file's log also gets the returned directory.
Screenshots can contain sensitive content even when the accessibility provider
suppresses protected values. On Unix, directories and files are created with modes
0700 and 0600. Windows uses the user's inherited ACLs.

### Command files

Command files contain one command per line, without the executable prefix:

```text
# Each command must succeed before the next runs.
run "C:\Program Files\App\app.exe"
wait window "App" --timeout 60
click --window "App" --text "Next"
assert state --window "App" --text "Ready" --timeout 30
snapshot "App"
screenshot --window "App" app.png
```

Single and double quotes group arguments. Double a matching quote to include it in a
quoted argument. Backslashes are literal Windows path separators. Blank lines and
lines that start with `#` are ignored. The file does not expand shell operators or
environment variables. Each line runs synchronously, and a nonzero exit stops the
file and becomes the script's exit code. The optional log receives command output,
line numbers, command names, start and end timestamps, and exit codes. Log headers
omit arguments, but command output can contain sensitive text. Command files reject
nested scripts and resident management.

### SSH access and the resident

GUI capability commands run over SSH without an installed resident on macOS, Linux,
and FreeBSD. On Linux and FreeBSD, an SSH login as the desktop's own user finds that
user's running graphical session and drives it directly, with no helper, resident,
or install. The command ignores a stale `DISPLAY` or one forwarded by `ssh -X` and
uses the real desktop. A root login acts in the logged-in user's session instead.
On macOS the command runs inside the user's session through a short-lived launchd
job of the same executable, because a plain SSH child cannot reach the window
server.

The resident is optional. macOS `run` commands use it, including `run` inside
command files, to launch an application into the Aqua session. It is also the only
thing that keeps a Wayland clipboard selection alive after a command exits. Without
it, `copy` sets the selection in a process that then exits, and a later `paste`
finds nothing unless a clipboard manager holds it. `karakuri-auto help` explains
these launch options without needing desktop access. Install the optional resident
as the desktop user:

```sh
karakuri-auto resident install
karakuri-auto windows
karakuri-auto run /path/to/application
```

SSH commands forward to a per-user Unix socket at
`/tmp/karakuri-auto-UID/control.sock`. That user owns its directory, with mode 0700,
and the socket has mode 0600. The resident runs commands with the GUI session's
environment and the SSH caller's working directory. It needs no password and no
network listener. Root cannot install another user's resident, and root commands act
in the user's session as described above. `--local` bypasses forwarding.

The resident keeps one desktop connection open, which preserves clipboard ownership
and portal consent after a command exits. Each command keeps its own working
directory, stdout, stderr, and exit code. The resident applies input from concurrent
commands one action at a time, but a command made of several actions can interleave
with another command. Disconnecting a command cancels its pending action. A lost
reply comes back as an error, and the resident never retries an action that may
have completed.

On portal desktops, the first input command requests keyboard, pointer, and monitor
sharing. Enable remote interaction in the consent dialog and select the monitors to
control. Absolute moves map desktop logical coordinates to the selected monitor's
stream and need the portal's optional position and size metadata. Coordinates
outside the shared monitors return an error. A portal without monitor sharing allows
only relative pointer movement. This backend cannot read the pointer position or
take screenshots through the portal. Later commands reuse the consent, but a grant
that covered only input, without monitor sharing, does not count, so the first
monitor-sharing session asks again. After you revoke a portal session, the next
command requests a new one, and failed actions are not replayed.

Linux and FreeBSD install the optional resident as an XDG autostart entry at
`~/.config/autostart/karakuri-auto.desktop`. Installing over SSH reads the display
and D-Bus settings from that user's running desktop processes. Install from a
graphical terminal if no desktop can be found. The resident exits when its display
goes away or is replaced. When a resident is listening, a same-user SSH command
forwards to it and reuses its long-lived session and clipboard ownership. When none
is, the command attaches to the session in its own process, so the resident is a
convenience, not a prerequisite.

macOS installs `~/Library/LaunchAgents/org.karakuri.auto.resident.plist` in the
user's Aqua launchd domain. The LaunchAgent runs the executable that performed the
installation, normally `~/karakuri-auto`, with `resident serve`. Grant Accessibility
and Screen Recording to that executable. These grants are separate from SSH or
Terminal grants. Keep the executable at its installed path. Build on macOS with
`MACOS_SIGN_IDENTITY=<identity>` to sign the CLI automatically, or sign a
cross-built upload with
`codesign --force --identifier org.karakuri.auto --sign <identity> karakuri-auto`
before installing the resident. An ad-hoc identity (`-`) works, but replacing an
ad-hoc signed build can mean granting permissions again. Use a stable signing
identity for repeated upgrades.

Upload a replacement under a temporary filename, rename it over the installed path,
then run `resident install` again. Linux and FreeBSD copy the executable to the
user's configuration directory, and macOS runs the installed CLI directly. The
installer restarts the listener and rejects an upgrade while commands are running.
`resident uninstall` removes the startup entry and stops the listener. Applications
launched with `run` keep running. Logs are in `karakuri-auto/resident.log` under the
user's configuration directory. Commands have a 30-minute deadline, up to 16
requests can run at once, and stdout and stderr are limited to 4 MiB each. Replies
arrive when a command finishes. A lost reply is an error and is never replayed
automatically.

### Windows

Windows desktop automation runs in the interactive user's session. Only a process
already inside that session, or one running as LocalSystem, can reach its desktop.
An administrator account is not enough. A `karakuri-auto` login that is not the
console user therefore forwards to a resident already running there. A
`karakuri-agent` installed as a user-scope logon task or a system service reaches
the desktop without it.

Install the resident once as the desktop user. An elevated SSH login works too:

```sh
karakuri-auto resident install
karakuri-auto windows
karakuri-auto run notepad.exe
```

Installation registers a per-user logon task and starts it if the user is signed in.
Task Scheduler only starts the resident, and commands travel over a local named
pipe. An SSH client outside the active console session forwards desktop commands to
that session automatically, whatever its session number. The resident runs commands
as its own user with its available elevation. The client verifies the resident's
user, session, and elevation, and the pipe admits only the resident user at its
privilege level, administrators, and SYSTEM. Connect over SSH as the console user,
because the client rejects a different user's resident. No network listener or
stored password is needed. A signed-out desktop has no resident until the user signs
in, and lock, unlock, login, and session commands run locally without it. `--local`
before the command bypasses forwarding.

Run `resident install` again after replacing the CLI executable. Installation copies
the executable under `%APPDATA%\karakuri-auto`, so uploading the public executable
does not overwrite a running file. An upgrade stops the old listener gracefully and
creates a new task instance. If commands are running, installation returns an error
asking you to retry after they finish. Launched applications keep running.
`resident uninstall` removes the logon task without ending those applications.
Versioned resident executables stay on disk for running jobs. Do not use
`schtasks /End` to stop the resident if its launched apps must survive.

Requests carry the caller's working directory and have a 30-minute deadline. Each
request runs in a separate process, with stdout and stderr returned to SSH and capped
at 4 MiB each. Output arrives when the command finishes. A lost reply is an error,
and commands are never replayed automatically. The resident accepts at most 16
concurrent requests. Relative paths, including script, screenshot, and log paths,
refer to the Windows host.

`karakuri-auto describe` lists the Windows backends for input, screen, windows,
clipboard, notifications, dialogs, speech, media, power, idle time, opening
applications, and login sessions. Notifications use toasts when available and fall
back to a tray balloon otherwise, including on Windows 7. A balloon notification
keeps the command running for up to 11 seconds, or until canceled, so the
notification can appear. Lock state is reported correctly on Windows 7 and Windows
Server 2008 R2.

The standard Go toolchain cannot build an executable that runs on Windows 7. Use the
[XTLS Windows 7 toolchain](https://github.com/XTLS/go-win7/releases), with a Go
version at least as new as the root `go.mod` requires. The tested Linux amd64 SDK is
`patched-1.27.1`, asset `go-for-win7-linux-amd64.zip`, with SHA-256
`4f0434fceea1b1c80c6d3511f0f777b0dc3b1260f172f1bf1ec202575bd36ec1`. Extract it
under `build/toolchains/win7` and build a separate artifact:

```sh
PATH="$PWD/build/toolchains/win7/bin:$PATH" GOTOOLCHAIN=local \
  make dev TARGET=auto GOOS=windows GOARCH=amd64 \
  AUTO_BIN=build/karakuri-auto-windows7-amd64.exe
```

`GOTOOLCHAIN=local` keeps Go from downloading an unpatched toolchain. Keep the
Windows 7 output separate from `build/karakuri-auto-windows-amd64.exe`, which uses
the standard toolchain. Make rebuilds only when sources change, so after switching
toolchains, remove the previous output or choose a new `AUTO_BIN`.

## No cgo

The module builds with `CGO_ENABLED=0` on every target, so `GOOS`/`GOARCH` is the
normal cross-compilation setup. Windows 7 needs a patched Go toolchain; see
[Windows](#windows) for the build command. The module reaches each platform through a pure-Go protocol library or the tools a session
already has, never a C binding.

## Testing

Run `make test TARGET=automation` from the repository root for the command and
module tests. On Linux or FreeBSD, run
`GOFLAGS=-tags=karakuri_integration make test TARGET=automation` to include the
portal and accessibility protocol tests. They use isolated `dbus-daemon` processes
and synthetic providers to check the input and session contracts and AT-SPI action
indices without touching the real desktop. Compositor consent, capture, and input
delivery still need checking on a live graphical session.
