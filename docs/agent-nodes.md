# Agent nodes

Agent nodes run with the operating-system permissions of the agent process. The
server namespaces each type as `agent.<agent id>.<type>`. An operator can remove a
type from an agent with its `disable` setting, and the agent then never advertises
that type to the server.

## Installing an agent

### Downloading the agent

Download the agent from
[GitHub releases](https://github.com/grmrgecko/karakuri/releases), using the same
release as your server. Each release has one agent archive per platform, named
`karakuri-agent-<version>.<os>-<arch>`:

| Host | Archive |
| --- | --- |
| Linux | `linux-amd64`, `linux-arm64`, or `linux-armv7` (32-bit ARM, such as older Raspberry Pi systems) |
| macOS | `darwin-arm64` (Apple silicon) or `darwin-amd64` (Intel) |
| Windows | `windows-amd64` or `windows-arm64`, as a `.zip` |
| FreeBSD | `freebsd-amd64` or `freebsd-arm64` |

Every archive except Windows is a `.tar.gz`. It holds the `karakuri-agent` binary
and nothing else to install.

### Installing for your user

Install the agent as the user it should act as, into your own binary directory,
and run its service as that user. This is the recommended install: the agent can
reach your desktop session, and it can update itself, which needs the service's
account to own the binary.

On Linux and FreeBSD, unpack the archive into `~/.local/bin`, which most
distributions put on the `PATH` once it exists:

```sh
mkdir -p ~/.local/bin
tar xzf karakuri-agent-<version>.linux-amd64.tar.gz
install karakuri-agent-<version>.linux-amd64/karakuri-agent ~/.local/bin/
```

If `karakuri-agent` is not found afterwards, add `~/.local/bin` to the `PATH` in
your shell profile and open a new terminal. On macOS use the same directory and
add it to the `PATH` in `~/.zprofile`. macOS builds are not signed, so a binary
unpacked from a browser download will not start until you clear its quarantine
flag:

```sh
xattr -dr com.apple.quarantine karakuri-agent-<version>.darwin-arm64
```

On Windows, extract the `.zip` into `%LOCALAPPDATA%\Programs\karakuri-agent` and
run `karakuri-agent.exe` from there, or add that folder to your user `PATH`.

Then mint a join token in **Settings > Agents** and run the command it shows, as
yourself, without `sudo`. It enrolls the agent and registers a service for your
account in one step:

```sh
karakuri-agent enroll --server karakuri.example:9443 --token <join token> --service
```

The service is a `systemd --user` unit on Linux, a launchd LaunchAgent on macOS,
and a scheduled task that starts at logon on Windows. On Linux a user service stops
when you log out, so run `sudo loginctl enable-linger <user>` once if the agent
should keep running while nobody is logged in.

Without `--service`, run `karakuri-agent run` to try the agent in the foreground,
and `karakuri-agent service install` and `karakuri-agent service start` to register
it later. The service command also takes `stop`, `restart`, `status`, and
`uninstall`. `--scope=user` and `--scope=system` override which service manager it
uses.

To run the agent in Docker instead, see [Running in a container](#running-in-a-container).

### Installing as root

Install as root only for a headless host, or when the agent must reach things your
user account cannot. A root agent runs every command and script as root. Its
desktop and GUI nodes still act in whichever user is signed in at the desktop,
through a helper it starts as that user, so anyone who can build flows with the
agent controls that person's desktop as well as the machine. On Windows the
equivalent is a system service, whose desktop nodes act as the console user.

Put the binary in `/usr/local/bin`, and enroll and install the service with `sudo`
too, so the identity and the service both belong to root:

```sh
sudo install karakuri-agent-<version>.linux-amd64/karakuri-agent /usr/local/bin/
sudo karakuri-agent enroll --server karakuri.example:9443 --token <join token> --service
```

The service is a system unit or launchd daemon. Manage it with `sudo`, as in
`sudo karakuri-agent service restart`. On Windows, run the enroll command from an
administrator prompt to get a system service.

### Updating

An agent running as a service checks GitHub releases once a day and installs a
newer one on its own: it downloads its archive, checks it against the release's
checksums, makes sure the new binary runs, swaps it in, and restarts through its
service manager. If the new version fails to stay up through three starts, the
previous binary is put back. The account the service runs as must be able to write
the binary, so a user install updates itself, a root install updates as root, and an
agent installed by root but run as a user logs that it cannot update. Turn it off
with `auto_update: false` in `agent.yaml`.

`karakuri-agent update` checks and installs now, then asks you to restart the
service, and `karakuri-agent update --check` only reports whether a newer release
exists. An agent in a container never updates itself; pull a newer image instead.

### Who can use an agent

The user who minted the enrollment token owns the agent. Other users do not see
it, and its nodes are missing from their palette, until the owner shares it from
**Share** on its card. A `use` share lets someone run the agent's nodes in their
flows; `edit` also lets them rename, describe, enable, and disable it; `admin`
also lets them share, revoke, and delete it. Holders of `api:agents:manage` reach
every agent. A flow deployed by someone who later loses access to the agent stops
running its nodes. Enrollment tokens are listed only to whoever minted them.

### Agent settings

The agent reads `agent.yaml` from the `etc/` directory beside its binary, then
`~/.config/karakuri/`, then `/etc/karakuri/`. Keys are the agent's flags with
dashes written as underscores, and a flag on the command line overrides the file.

| Key | Default | Description |
| --- | --- | --- |
| dir | follows the config file | Directory holding the agent's key, certificate, and identity. |
| server | unset | Server to dial, `host:port`. Required to enroll; overrides the enrolled address when running. |
| timeout | 60 | Default time limit for one node execution (seconds). |
| max_output | 262144 | Cap on one file, clipboard, or log-line read (bytes). |
| disable | unset | Node types this agent must not offer, as a list. A name that matches no node type stops the agent. |
| log_level | info | `debug`, `info`, `warn`, or `error`. |
| log_format | json | `json`, or `console` for human-readable output. |
| auto_update | true | Install newer releases automatically when running as a service. |
| update_prerelease | false | Also update to releases marked as pre-releases. |

```yaml
server: karakuri.example:9443
disable:
  - power
  - clipboard-write
```

The identity lives with the configuration file that names it:
`/etc/karakuri/agent.yaml` keeps it in `/var/lib/karakuri/agent`, an
`etc/agent.yaml` beside the binary keeps it in `var/agent` there, and a file
anywhere else keeps it in an `agent` directory beside the file. With no
configuration file it goes in `~/.config/karakuri/agent`. A service records the
directory and server address when it is installed.

The agent has no sandbox. Its nodes can read and change whatever the account it
runs as can, so pick that account deliberately. To confine an agent to `/srv`, run
it as a user who can only write there. To give two people on one machine their own
nodes, run one agent as each of them.

## Enrollment and renewal

Enrollment refuses a directory that contains `agent.json`. A directory without that
file can hold files from a failed attempt, and retrying generates and overwrites the
local key. The server uses up the join token as soon as it accepts it, so a failed
or interrupted enrollment can need a new token even though the host has no complete
identity.

The agent checks for renewal at startup and every six hours. A failed check retries
at the next interval, so an unreachable server or a failed disk write can still let
a certificate expire. Renewal keeps the private key, and a failed renewal leaves the
previous certificate working. The agent uses the new certificate on its next
connection. The server closes a live session when its certificate expires, and the
agent reconnects with the renewed one.

When the server rotates its agent CA, an agent picks up a certificate from the new
root on its next renewal check, and a later check repairs a failed one. A host that
has not connected with a certificate from the new root before the old root's last
30 days must re-enroll.

**Settings > Agents** shows when the agent CA and its successor expire, and
whether a rollover is in progress. **Rotate CA** starts a rollover early. Press it
if the CA key may have been exposed. Connected agents move to the new CA as they
renew.

A backup carries the agent CA and the server's identity, and a restore keeps them.
Enrolled agents reconnect to the restored server without enrolling again, on the
same machine or a new one. A new machine must answer at the address the agents
enrolled against.

## Running in a container

The `grmrgecko/karakuri-agent` image runs an agent in a container, on amd64
or arm64. Mint a join token in **Settings > Agents**, then start it with the
server's agent address and the token:

```sh
docker run -d --name karakuri-agent \
  -e KARAKURI_SERVER=karakuri.example:9443 \
  -e KARAKURI_JOIN_TOKEN=<join token> \
  -v karakuri-agent:/var/lib/karakuri \
  grmrgecko/karakuri-agent
```

The first start enrolls and stores the identity on the volume. Later starts,
including a new container on the same volume, reconnect as the same agent and
ignore the token. Without the volume, a recreated container has no identity and
needs a new token. `KARAKURI_AGENT_NAME` sets the agent's name, which is the
container's hostname otherwise.

The agent's nodes act on the container, not on the machine running it. Commands
and scripts run as the container's `karakuri` account and see its filesystem,
processes, and network, plus whatever you mount or publish into it. Desktop and GUI
nodes have no screen or session to reach, and fail with that error when a flow runs
them. To keep them out of the palette, mount an `agent.yaml` over
`/etc/karakuri/agent.yaml` that lists them under `disable`, keeping the image's
`dir: /var/lib/karakuri/agent` line. For tools the image lacks, build your own on
top of it:

```dockerfile
FROM grmrgecko/karakuri-agent
USER root
RUN apt-get update && apt-get install -y --no-install-recommends curl jq && rm -rf /var/lib/apt/lists/*
USER karakuri
```

## Execution and files

Script receives the message as JSON on stdin and in the `PAYLOAD`, `TOPIC`, `META`,
`VARS`, and `INPUT` environment variables. A variable longer than 32,000 bytes is
left unset and written to a file in the script's temporary directory instead, with
`PAYLOAD_FILE`, `META_FILE`, and so on holding the path. Linux refuses to start a
program when one environment string passes 128 KiB, and Windows caps a variable at
32,767 characters, so a large payload would otherwise stop the script from starting.

Script picks its interpreter from the first line's shebang. A full path
(`#!/usr/bin/env python3`, `#!C:\Python311\python.exe`) runs that program, and a
bare name (`#!python3`, `#!node`, `#!powershell`) is found on the `PATH` of the
account the agent runs as, trying each `PATHEXT` extension on Windows. If the agent
cannot find an interpreter, add it to that account's `PATH` or name it by full
path. PowerShell and `cmd`
get the arguments and file extension they need (`-File` with a `.ps1` file, `/c`
with a `.bat` file) whether the shebang names them bare, with `.exe`, by full path,
or in another letter case, as in `#!C:\Windows\System32\cmd.exe`.

Run Command passes each argument to the program as written after `{{ }}` rendering,
including leading and trailing whitespace, so a lone space is a valid argument. Open
Application passes its arguments the same way, and Command and Script pass each
environment value the same way.

Command and Script never retry automatically, because repeating a program can
repeat its side effects. By default they return stdout and stderr, up to 32 MiB
each. Each stream can instead use `file` mode with its output path, or `discard`
mode. A file-only stream writes straight to the file without the return limit, and
a discarded stream writes nothing. In return mode, `stdout_path` and `stderr_path`
also mirror the streams to files on the agent host, and the 32 MiB return limit
still applies. The agent buffers returned output on disk while the process runs, so
a large stream does not have to fit in memory.

Canceling either node terminates its process tree. `termination_grace` (Termination
grace) defaults to `0s`, which forces termination immediately. Set a duration such
as `500ms` or `5s`, up to `1m`, to allow cleanup on Unix. The process and its
descendants receive SIGTERM, and anything still running when the grace expires
receives SIGKILL. Cleanup still reaches descendants when the parent exits first,
including with file-only and discarded output, and descendants that started their
own session with `setsid`. It does not reach a process that was already orphaned to
init before cancellation. Exited descendants that nobody has reaped can use up the
full grace period. Normal completion does not wait for grace.

Cancellation and timeout stay failures even if the process exits zero, or with an
accepted code, during cleanup. An invalid grace fails before launch and before any
output file changes. Windows has no general cooperative shutdown signal for service
agents, so a positive grace fails before launch there. Use `0s` on Windows. There,
cancellation terminates every descendant, including ones whose parent already
exited. If cleanup fails on any platform, the node's error says so and warns that
descendants may still be running. Processes left behind by a command that completes
normally keep running on every platform. A leftover process that keeps a mirrored
stream or standard input open holds the node for up to five seconds after a zero
exit, and the node then succeeds with the output written by then.

Accepted exit codes are a nonempty JSON integer list, `[0]` by default. An explicit
list overrides Run Command's Ignore the exit code setting. Launch failures,
cancellation, and timeouts still fail. The property result destination stores
decoded stdout at a path under `payload` or `meta`, such as `meta.command_result`,
and keeps the rest of the input. Existing parent values on that path must be
objects. File-only or discarded stdout leaves the payload alone and skips decoding.
Metadata keeps the exit code, returned stderr, and configured paths.

The 32 MiB limit applies only to returned streams. The on-disk buffer and file
mirrors have no size limit, so watch disk space on the agent host. Every node
execution has an overall deadline: its own timeout, or the default its timeout field
declares when the configuration omits one, or the agent's default of 60 seconds,
plus ten seconds for cleanup. Command and Script add their termination grace, and
Service Control adds its confirmation timeout. When a run is canceled, the node
stops waiting immediately while process cleanup continues for its grace. The agent
abandons a node stuck in an operating-system call it cannot interrupt at the
deadline instead of stopping it. Triggers run for the life of their subscription and
have no execution deadline.

Write File in replace mode, and Copy or Move with overwrite enabled, write a
complete staging file next to the target and then swap it into place. If the swap
fails, the agent restores the previous target, and a failed restore reports both
errors. A crash or power loss mid-replacement can still leave the target in an
intermediate state. Append and delete act on the file directly. Write File also has
a create-only mode, which fails if anything already exists at the path and never
overwrites. Create-only needs a filesystem with hard links and fails on any other.

Copy and Move compare source and destination with symlinked directories resolved,
so they refuse a path that reaches the source through a link just as they refuse the
source itself. Copy refuses FIFOs, sockets, and device files, which have no content
to read to an end.

Write File refuses to replace a directory, so a file name that renders empty cannot
replace its parent directory and delete what was in it. It also refuses to replace a
FIFO, socket, or device file, because the replacement would turn it into a regular
file. Append mode writes into one. Delete mode removes files and links only and
refuses any directory, empty or not. In replace mode Write File follows a symlinked
path, replacing the file the link names and keeping the link. A link whose target
is missing is replaced by the file. The agent removes staging and backup files even
when a copied directory is read-only.

List Files and a recursive Watch Files descend into a directory named through a
symlink and report paths under the name the node was given. They do not follow
symlinks inside the tree, including one a watched tree gains after the watch starts.

Replacement keeps existing permissions by default. Disabling `preserve_permissions`
sets mode 0644, as for a new file. On Unix a preserving replacement also keeps the
file's owner and group. Only a root agent can keep another account's ownership. Any
other agent keeps the group when it belongs to that group, and otherwise the
replacement is owned by the agent. Copy and Move give each copied file its source's
permission bits regardless of the agent's umask, and the copy belongs to the agent's
account.

`expected_sha256` requires an existing regular file with the given 64-digit digest.
The agent checks before staging and again just before the swap, and rejects
symlinks. This catches stale content but is not an atomic compare-and-swap. Another
process that changes the file after the final check can still race the replacement.

Read File, Command, Script, and HTTP request return an empty array when text read as
lines has no lines, as the server's Read File does.

File Details reports existence, type, size, modification time, mode, and
permissions. SHA-256 is optional because calculating it reads the whole file.

## Triggers

Tail Log follows truncation and rename-based rotation. It saves a checkpoint on the
server with the file's identity, path, last complete-line position, and last
activity, so it resumes where it left off after an agent or server restart. It saves
every 15 seconds by default, and inactive records expire after 14 days. A crash can
therefore replay up to one interval, and Tail Log reads an incomplete last line
again after a restart. It reads a file from its beginning when the file was replaced
while the agent was offline, or did not exist when the trigger was armed. An
oversized line emits a prefix capped at the agent's `max_output` limit and discards
the rest through the next newline, even when that line was not complete at the time
of clipping.

A file that was truncated or rewritten in place is read again from its beginning,
even when it has grown past the old position since the last poll. Tail Log detects
this even when the rewrite happened while the agent was stopped. On rotation it keeps
reading the old file until nothing is left, reports the old file's unterminated last
line, and then reads the replacement. Lines a writer appends to the old file after
that are lost, as are old-file lines left unread when the agent restarts during that
drain. `max_lines_per_poll`, 1,000 by default, bounds each poll across both files.

Watch Processes compares process identity on each poll and can emit start and stop
events. PID and creation time form the identity, so a reused PID counts as a new
process.

Agent Connection is a server-side trigger. It emits authenticated connect and
disconnect events with the agent id, name, state, and timestamp, and can filter by
exact id or name.

## System and network

Network Probe checks a TCP address or HTTP URL from the agent's network view. An
unreachable target normally emits `reachable: false` and `healthy: false`. HTTP
probes support GET and HEAD, headers, expected status codes or inclusive ranges such
as `200-299,304`, and a redirect policy. An optional case-sensitive `body_contains`
requires GET and a complete response within the agent's `max_output` limit. An
unexpected status or body reports `reachable: true` and `healthy: false`. With no
expected statuses or `body_contains` set, any response counts as healthy, an HTTP 500 included.
Enabling `fail_if_unreachable` also fails unmet health assertions. Invalid settings,
incomplete body reads, and parent cancellation always fail.

HTTP request is an execution node in each agent's System palette. It sends GET,
HEAD, OPTIONS, POST, PUT, PATCH, or DELETE from the agent's network. URL and header
values accept interpolation. `body_property` selects message bytes, text, or JSON,
and an empty value sends no body. Body format defaults to text for strings and JSON
for objects and arrays. Select Binary to decode base64 binary message payloads into
request bytes. JSON format also serializes scalar values. For these explicit formats
the content type defaults to JSON or octet-stream, and configured headers override
it. A configured `Host` header sets the host the request names, so a URL can address
a server by IP and still select one of its virtual hosts. Responses use auto, JSON,
text, lines, or binary format, replace the payload, and add status, response
headers, and `duration_ms` to metadata. Empty HEAD, 204, and 205 responses decode as
null in auto and JSON modes.

Request statuses default to 200-299. Configure `100-599` to accept every response.
Timeout defaults to 30 seconds. `max_output`, or a smaller `max_response_bytes`,
bounds the response body, and overflow fails instead of truncating. Requests never
retry automatically, GET included. Both HTTP nodes validate TLS unless configured
not to, follow at most ten redirects by default, and drop configured headers when a
redirect changes origin. With redirects disabled, the node returns the redirect
response. Requests go straight from the agent host and ignore proxy environment
variables. Transport errors and debug metadata omit request URLs and headers, but
response headers and bodies still appear in run history.

Service Status and Service Control use systemd on Linux, launchd on macOS, and the
Service Control Manager on Windows. Control supports start, stop, restart, enable,
and disable, and never retries automatically. Optional `confirm` polls the observed
state after issuing the action once. Start and restart require running, stop
requires stopped, and enable and disable require the matching startup
configuration. Confirmation defaults to 30 seconds with a 250 ms interval and
extends the node's deadline. An unknown or transitional state never counts as
success. Metadata contains `confirmed: true` and the final `service_status`
observation. Confirmation does not prove application health or that restart started
a new process. On Windows a service counts as enabled when its startup type is not
disabled, including manual start.

On Windows, Service Status reports `state` and `running` and has no raw `details`
text. Read those fields to decide what to do.

Host Info includes address summaries and per-interface addresses, CIDRs, scope, MAC
address, MTU, link state, and loopback state. It reports the default gateway when
the platform exposes one, along with the selected desktop automation backends or
their initialization error.

## Desktop and GUI

Media Status reads playback state and track details from the desktop's media players
on Linux and FreeBSD, the same players `playerctl` sees, and through Music or Spotify
on macOS. On Windows it reports that media
status is unavailable, and Media Control uses system media keys there.

Media Control's Set volume applies 50 when the configuration names no volume. An
explicit 0 silences the output.

Window List includes geometry when the desktop backend exposes it. Screenshot can
capture the active window when geometry is available. Mouse can move before clicking
and can interpret coordinates relative to a selected window.

Read Clipboard caps its payload at `max_output`. Metadata includes the returned
size, the original size, and whether the payload was truncated.

Dialogs have no secret-entry mode. Run history stores node output, so returning a
password would persist it. Use credentials for secret material.

### GUI flow nodes

The GUI palette has these execution nodes. Each accepts an input message and has an
error output. Read nodes replace the payload with the current observation. Click
Control and Invoke Menu keep the input payload and add result metadata. Mutating
operations never retry automatically.

| Node | Behavior |
| --- | --- |
| UI Snapshot | Captures the time, window metadata, backend names, and accessible elements. |
| Click Control | Clicks a unique native label or invokes an accessible element ID. |
| Wait for Window | Waits for an unambiguous window to appear, or for all matching windows to disappear. |
| Wait for UI State | Waits for accessible text, role, ID, enabled state, absence, or stable pixels. |
| Menu List | Reads the current native or accessible menu tree, IDs, paths, and reported state. |
| Invoke Menu | Invokes one enabled menu path or command ID. |
| Display List | Reads the current output geometry and modes reported by the backend. |
| Set Display Mode | Requests a temporary primary-output width, height, and color depth. |
| Dialog List | Lists application dialogs and their controls, with per-window inspection failures. |
| Screenshot Now | Captures fresh pixels when the execution message arrives. |
| Window List Now | Reads fresh window metadata when the execution message arrives. |
| Drag and drop | Drags the pointer along a timed path and always releases the button. |
| Window placement | Restores, moves, resizes, or sets bounds, and reports freshly observed geometry. |
| Image match | Matches a same-scale reference and reports an RGB agreement percentage. |

Screenshot and Window List are value sources, and a run reads each of them only
once. Use the Now variants after an action when later steps need another
observation. Screenshot variants support screen, region, active window, and selected
window modes. Window capture first asks for the window's own pixels, which can
capture a covered window without activating it, and falls back to the visible
desktop rectangle, borders and overlapping windows included, when those are
unavailable. Wayland always captures the visible rectangle and needs window
geometry from the compositor. Region captures can use display-relative
coordinates (the default) or desktop coordinates across displays, including negative
origins.

Keyboard types its text as written after `{{ }}` rendering, including leading and
trailing whitespace. A line break presses Return and a tab presses Tab wherever they
appear. Keyboard can select a window with the same filters as Screenshot. A selected
target must have focus before input is sent. Activate Target Window brings it
forward and waits up to two seconds for observed focus. Without activation, an
unfocused target fails immediately. With no selector, input goes to the current
focus. Focus can still change after the check if another application or user acts.

A window ID overrides active-window selection, title and class patterns, and every
other filter. Window match mode selects regular expressions (the default, in Go
regular expression syntax), literal contains, or exact case-sensitive matching.
Additional title and class constraints use the same mode, and PID and executable
constrain the process. Executable accepts an exact path or filename when the
provider reports it. Actions reject multiple matches, so narrow the pattern or
select an ID when more than one window matches. Window List still returns every
match. Window payloads include the executable image, a nullable enabled state, and
`geometry_known` alongside optional geometry. Window and Mouse selectors, snapshot
selectors, labels, menu targets, and evidence directories accept message
interpolation with `{{ }}`.

UI Snapshot includes visible elements by default. Include Hidden Elements adds
hidden ones, and Only Enabled Elements drops disabled ones. Source selects
accessibility (the default), native controls, or both. Windows native controls carry
HWND IDs, immediate parent HWND IDs, and native class names. Unix and macOS map
their accessible controls. Role and Text Contains filter each source. Maximum
Results limits each filtered source, zero returns every provider result, and
`truncated_sources` reports which output was capped. The limit bounds returned
output, not how long the provider takes to enumerate. IDs and roles keep the
provider's spelling. Snapshots omit protected values, but screenshots can still show
sensitive text.

Click Control prefers an exact label over a unique substring. Element ID overrides
the label, and the node looks it up again just before the click. Role and Immediate
Parent ID constrain the target before acting. Label Match selects exact-first (the
default), exact, or contains matching, and keeps case sensitivity. Native Windows
role constraints use class names, and clicks accept native control IDs from
snapshots. Native Windows labels omit accelerator ampersands and shortcut suffixes.

Wait nodes default to 30 seconds and accept a positive timeout up to one day,
including fractional seconds. Check Once returns `matched: false` for an unmet
condition without polling. On Timeout can fail (the default) or return
`matched: false`. Ambiguity, provider errors, and parent cancellation stay errors.
Check Once cannot establish pixel stability. Enabled State selects either, enabled,
or disabled. Without `enabled_state`, the node honors an `enabled` setting.

Wait for UI State selects an existing window and keeps waiting on that window. Text
is a case-insensitive substring, and role is an exact case-insensitive provider
role. Conditions apply to one visible element, and a positive match must be unique.
Failed inspection never satisfies an absence condition. Pixel stability is a
separate mode and cannot be mixed with element conditions. Region coordinates are
relative to the selected window. Stable pixels do not prove that an application has
finished its work.

By default, a failed state wait tries to capture evidence once a window has been
selected. It saves `snapshot.json` and `screenshot.png` under the agent account's
configuration directory at `karakuri-agent/captures`, or under the configured
evidence parent. The error names that directory and any capture failure. Capture
has an eight-second limit within the node's cleanup allowance. Run cancellation
skips evidence. Captures stay until you remove them.

Dialog List recognizes Win32 `#32770` dialogs, macOS accessibility dialogs and
sheets, and Unix accessibility dialog and alert roles. Entries in its
`inspection_errors` map are windows it could not inspect, so an empty `dialogs` list
alone does not prove no dialog exists. Filter Windows applies the shared selector
before inspection and allows multiple matching windows for discovery. Disable
Include Controls to omit control details. Custom providers can omit menu paths,
controls, or geometry.

Display mode changes affect the primary output only. On Unix, use the reported color
depth. An unsupported mode fails without claiming a change.

### Drag, placement, and image matching

Drag and drop holds the left, middle, or right button along a timed path. Duration
runs from zero through thirty seconds and defaults to 0.5. Coordinates are absolute
desktop coordinates. Coordinates are inside a window makes them relative to a
uniquely matched window's reported origin. Other input actions from the same agent
wait until the drag finishes, and the agent always tries to release the button,
with two seconds allowed for the release. A canceled drag can still complete its
path after the node reports cancellation. Separate agent processes on one host do
not coordinate their input.

Window placement selects one window and by default restores it before setting
position and size. Move, Resize, and Restore are also available. The output contains
`requested`, freshly observed `window` geometry, and, for geometry requests,
`bounds_matched`. Application minimum sizes and window-manager constraints can
produce `bounds_matched: false`. Geometry follows the selected backend's window
bounds. X11 reports client bounds, and Windows and macOS report native window
bounds. macOS restores minimization and clears fullscreen and zoom state when the
application exposes those attributes as writable. It cannot infer zoom state the
application omits. Hyprland and Sway require the window to be floating already.
Other Wayland compositors can restore a window but cannot move or resize it. A
window known only through accessibility cannot be placed. Actions reject ambiguous
targets.

Image match searches a fresh capture of the screen, a region, or the selected or
active window, or a Binary image input value socket. Reference image selects a PNG
or JPEG file on the agent host or a Binary reference input socket. A selected Binary
socket needs a value edge. Reference paths accept `{{ }}` expressions. Matching
needs no OCR, model, or extra software, and never clicks a result automatically.

Minimum match defaults to 98 percent and accepts values greater than zero through
100, fractions included. The score is `100 * (1 - mean absolute RGB difference / 255)`.
It measures pixel agreement, not recognition probability, and 100 requires identical
RGB pixels. Reference images must be opaque and at the same scale. Source alpha is
ignored. Theme, display scale, and rendering changes affect the score.

Results report `coordinate_space: "image_pixels"`, source dimensions, match bounds,
`match_percent`, and `truncated`. Coordinates start at the captured image's top
left. `capture.mapping_known` says whether the backend supplied a reliable mapping to
desktop coordinates. When it did, `capture.desktop_mapping` gives the origin and
desktop units per pixel for X and Y separately, `capture.desktop_bounds` gives the
captured rectangle, and each match adds `desktop_bounds` and `desktop_center`.
Windows and X11 screen and region captures use physical desktop pixels, and macOS
maps image pixels to desktop points. Clipped regions report their actual origin.
Binary inputs, portal captures, and window captures without reliable geometry report
an unknown mapping and omit desktop coordinates.

An empty match list from a completed search is a success. Overlapping candidates
merge at an intersection-over-union above 0.3. Maximum results defaults to ten and
accepts one through 100. The scan runs top to bottom, left to right, and stops at
the next distinct match past that limit. Returned candidates sort by score. A
truncated search does not claim to have found the best matches across the whole
image.

Encoded files and inputs are limited to 32 MiB, source images to 16 Mi pixels,
references to one Mi pixels, and searches to 100 million pixel comparisons. Running
out of that comparison budget is an error, never an empty no-match result. Shrink
the capture region or the reference when that happens. Canceling the run stops a
search in progress.

### Automation command files in flows

Automation Script, in the Exec palette, runs an existing command file or inline
commands through a configured `karakuri-auto` executable on the agent host. The
executable field defaults to `karakuri-auto` in PATH. Command File and the optional
Command Log take host-local paths and accept message interpolation. Working
directory, environment, output mirrors, timeout, and process-tree cancellation work
as in Run Command. Output is text, with the same 32 MiB cap per returned stream. A
nonzero exit fails the node, and the node never retries automatically. Inline
commands are literal text with no message interpolation. The agent writes them to a
private temporary command file and removes it after success, failure, or
cancellation.

Install the executable separately and configure its desktop access. The
`karakuri-auto` README in its release archive covers its commands, command-file
syntax, and desktop setup, and `karakuri-auto help` lists them on the host. Command
files run their commands in sequence and stop at the first failure. A detached
application launched by `run` can outlive the script. Use individual GUI nodes when
steps need separate error branches or their own run-history observations.

### Desktop session access

An agent running as a user attaches to that user's own graphical session, and an
agent running as root acts in the logged-in user's session. An unprivileged agent
can use only its own graphical session. The agent looks the session up again
whenever it needs it, so an agent that started at boot before anyone logged in
works once someone does, and it follows a logout and a new login. Until a user is
at a desktop, desktop nodes fail with a message saying so, and session nodes keep
working. On macOS, grant the agent Accessibility and Screen Recording under
**System Settings > Privacy & Security**. Reaching the session does not bypass
those grants.

When the agent acts in another user's session and loses contact with it mid-call, it
retries the call once, so an action that completed before contact was lost can run
twice. Canceling the node stops waiting, but an action already started in the
session can still finish.

On Linux and FreeBSD an agent finds the session without any helper installed, and
it ignores a stale or forwarded `DISPLAY`. On macOS it reaches the window server
through a short-lived launchd job running the same executable.

On Windows only a process inside the interactive user's session, or one running as
LocalSystem, can reach that session's desktop. An agent installed as a user-scope
logon task already runs in the session. An agent installed as a system service
reaches it too: the first time a flow uses a desktop node, it starts a helper as
the console user, on that user's visible desktop, and stops the helper when it is
no longer needed. Nothing beyond the service needs installing. Desktop actions run
as the console user, not as SYSTEM. Session nodes, which list sessions, lock, switch, and unlock with
credentials, run as SYSTEM. A signed-out console has no user to run the helper as,
so a desktop node reports that until someone signs in.

## Browser questions

The server's Browser prompt node uses an agent's Open URL capability to show
Markdown instructions, attachments, and author-defined response buttons. The wait
for a person's answer happens on the server and does not count against the agent's
execution deadline. Configure a server URL the agent's browser can reach, and keep
the agent in an interactive desktop session. No agent update is needed. See
[Browser prompts](browser-prompts.md) for authoring, buttons, timeouts, and access.
