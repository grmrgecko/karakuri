<p align="center">
  <img src="frontend/public/favicon.svg" alt="Karakuri icon" width="160" height="160">
</p>

# Karakuri

[Website](https://karakuri.moe) · [Documentation](https://karakuri.moe/docs.html) ·
[Releases](https://github.com/grmrgecko/karakuri/releases)

Karakuri is a home automation flow engine and dashboard. A flow wires triggers,
logic, and actions together on a canvas, and every node can carry a retry policy
with attempts, delay, and backoff. A node that still fails can route its message
onto an error branch, and a flow can name an error-handler flow that runs when it
fails. The editor is a node graph built on Svelte Flow. Flows work with Home
Assistant, openHAB, ESPHome, MQTT and Zigbee2MQTT devices, and Frigate cameras.
Remote machine agents bring the rest of the house's computers into the same
flows: a NAS, a Windows PC, a Mac, or a Raspberry Pi enrolls once, and its
commands, files, services, network, and desktop become nodes beside the lights
and sensors. Dashboards built in the same app show live state and control
devices from wall tablets and browsers, and the server can render any dashboard
to an image for screens that cannot run a browser.

## Features

- **Flow editor.** Drag nodes onto a canvas, fill in each node's config form, and
  watch validation and the run panel update node states while a run executes.
- **Retries and failure routing.** Each node takes a retry policy with max
  attempts, delay, exponential backoff, and a delay cap. A node that exhausts its
  retries can send the message out its failure branch so the run continues, and a
  flow can name an error-handler flow that runs on unhandled failures.
- **Remote machine agents.** Install the agent on another computer and enroll
  it with a one-time join token, and that machine's capabilities show up in the
  editor as nodes that run on it:
  - Commands and scripts with the machine's own interpreters (Bash, Python,
    Node, PowerShell, cmd), with output returned to the run and the process tree
    stopped when the run is cancelled.
  - Files: read, write, list, watch a directory, and tail a log that keeps its
    place through rotation and restarts.
  - Services and processes: systemd, launchd, and Windows services with
    confirmed state changes, process lists, and triggers on processes starting
    or stopping.
  - Network probes and HTTP requests sent from that machine's network.
  - The desktop: keyboard, mouse, screenshots, windows, clipboard,
    notifications, dialogs, media players, and the screen lock, plus GUI
    automation through the accessibility tree and on-screen image matching.
  - Questions put to a person in that machine's browser, with the flow
    continuing on their answer.

  Agents run on Linux, macOS, Windows, and FreeBSD on amd64 and ARM, including
  32-bit Raspberry Pi, or in a container. They connect over mutual TLS, belong to
  whoever enrolled them until shared, can have any node type turned off, and
  update themselves. An agent acts as the account it is installed for and is not
  sandboxed.
- **Drafts and deployed versions.** Edits go to a draft. Deploy freezes an
  immutable numbered version, and triggers arm against that version. Version
  history restores any version back into the draft in one click.
- **Test runs.** Run the draft from the editor, replay a run from any node with the
  last run's outputs as its inputs, cancel live runs, and re-run the last run's
  trigger payload against the deployed graph.
- **Subflows.** Group nodes into a local subgraph, or promote it to a shared library
  subflow that other flows reuse. Subflows declare interface ports.
- **Export and import.** Download any flow as a self-contained JSON document, with
  shared subflows embedded and declared connection fields cleared, and import it on
  another instance.
- **Sandboxed scripts.** The script node runs JavaScript, Python, or Bash in
  interpreters with no filesystem, process, or environment access. HTTP goes
  through the host and follows a three-level network policy. Flow scripts in all
  three languages can call eligible nodes through generated functions or commands,
  with completion, hover, and verification in the editor. A synchronous webhook can
  let a script compute the HTTP response.
- **Integrations.** Home Assistant entities, services, events, areas, and
  Assist conversations; openHAB items; ESPHome nodes over their native API;
  MQTT topics and devices, with Zigbee2MQTT and Home Assistant MQTT discovery
  devices added in one step; Frigate cameras, events, and reviews. Connections
  also reach Prometheus, InfluxDB, and PostgreSQL for history and charts, ICS,
  CalDAV, and Google calendars, notification services, AI providers, and MCP
  servers. Every connection's credentials are encrypted at rest and its status
  is live.
- **Device auto import.** A connection can add every device it reports on its
  own, including ones that appear later, and remove the ones it has not
  reported for a set time.
- **Dashboards.** A grid editor for wall-tablet control panels built from
  web-component widgets: clocks, media players, cameras, charts, buttons, and
  entity cards with live state and tap-to-toggle for Home Assistant entities,
  openHAB items, ESPHome and MQTT devices, and Karakuri controls. Each user picks
  one of 31 bundled CSS themes, and a dashboard can be shared with users, groups,
  or the public.
- **Dashboard renders.** The server turns any dashboard into a PNG, JPG, PDF, GIF,
  BMP, or TIFF at the size a URL names, for e-ink panels, photo frames, wiki pages,
  and chat messages. Schedules render on their own and serve the latest image from
  a fixed address. A render can only view its dashboard, never press a control.
- **MCP.** An AI assistant can operate Karakuri over the Model Context Protocol at
  `/mcp`. It can author and deploy flows, read per-node run failures, and manage
  controls, connections, devices, and dashboards. The same permissions the API
  enforces apply, plus an `api:mcp:use` permission an admin grants per group.
- **Operations.** Prometheus `/metrics`, structured logging, backups, role-based
  permissions, API keys, and a WebSocket event stream.

## Getting started

[docs/getting-started.md](docs/getting-started.md) covers every install method
and database in detail. The short versions follow.

### Install a release

Download the server archive for your platform from
[Releases](https://github.com/grmrgecko/karakuri/releases). It runs on Linux and
macOS (amd64 and arm64) and on Windows (amd64). On Linux or macOS, unpack it and
install:

```sh
tar xzf karakuri-<version>.linux-amd64.tar.gz
cd karakuri-<version>.linux-amd64
sudo ./install.sh
sudo karakuri service install
sudo karakuri service start
sudo -u karakuri karakuri user create admin admin@example.com <password> --admin
```

Open http://localhost:8080 and sign in. `install.sh` puts the binary in
`/usr/local/bin`, the plugins and themes in `/usr/share/karakuri`, a configuration
with fresh secrets in `/etc/karakuri`, and the database in `/var/lib/karakuri`.
Run it without `sudo` to install under your home directory instead, then run the
`karakuri service` commands as yourself.

On Windows, or to run without installing, start it from the unpacked directory:

```sh
mkdir etc
./karakuri config gen --env=prod > etc/config.yaml
./karakuri user create admin admin@example.com <password> --admin
./karakuri serve
```

The generated file carries placeholder secrets that every copy shares. Replace
`jwt_secret` and `encryption_key` in it with the output of `openssl rand -hex 32`
before anyone else can reach the server. `install.sh` and the Docker image do this
for you.

macOS builds are not signed. If macOS refuses to open a binary from a browser
download, clear the quarantine flag on the unpacked directory:

```sh
xattr -dr com.apple.quarantine karakuri-<version>.darwin-arm64
```

A system install updates itself from a daily root timer that `karakuri service
install` adds. `sudo karakuri update` installs a newer release now and restarts the
service on it. To upgrade by hand instead, unpack the new archive, run
`sudo ./install.sh` again, and restart with `sudo karakuri service restart`. Your
configuration and database stay as they are.

### Run with Docker

Docker Compose is the recommended way to run the image. Save
[compose.yaml](compose.yaml) from this repository into an empty directory, then
start it and create the first admin account:

```sh
docker compose up -d
docker compose exec karakuri karakuri user create admin admin@example.com <password> --admin
```

The compose file restarts the container after a reboot and keeps the
configuration and database in named volumes. To use PostgreSQL instead of
SQLite, save [compose.postgres.yaml](compose.postgres.yaml) as `compose.yaml`
and put a database password in a `.env` file beside it before starting:

```sh
echo "KARAKURI_DB_PASSWORD=$(openssl rand -hex 24)" > .env
```
 `docker compose pull`
followed by `docker compose up -d` moves to a newer image.

Without Compose, the same container starts with `docker run`:

```sh
docker run -d --name karakuri --restart unless-stopped -p 8080:8080 -p 9443:9443 \
  -v karakuri-data:/var/lib/karakuri -v karakuri-config:/etc/karakuri \
  grmrgecko/karakuri
docker exec -it karakuri karakuri user create admin admin@example.com <password> --admin
```

The image is published for amd64 and arm64 and writes a configuration with fresh
secrets on first start.

### Add a remote agent

An agent puts another machine's commands, files, services, and desktop into your
flows. Download the agent archive from
[Releases](https://github.com/grmrgecko/karakuri/releases) and install it for the
user it should act as, then mint a join token in **Settings > Agents** and run the
command it shows on that machine, without `sudo`:

```sh
mkdir -p ~/.local/bin
tar xzf karakuri-agent-<version>.linux-amd64.tar.gz
install karakuri-agent-<version>.linux-amd64/karakuri-agent ~/.local/bin/
karakuri-agent enroll --server karakuri.example:9443 --token <join token> --service
```

The agent runs as a service for that user, so it reaches their desktop and can
update itself. The [agent guide](docs/agent-nodes.md) covers installing as root, which
runs every command as root and needs `sudo` for both enrolling and the service.

The agent also runs in a container, acting on the container rather than the host:

```sh
docker run -d --name karakuri-agent \
  -e KARAKURI_SERVER=karakuri.example:9443 \
  -e KARAKURI_JOIN_TOKEN=<join token> \
  -v karakuri-agent:/var/lib/karakuri \
  grmrgecko/karakuri-agent
```

## Documentation

The guides in [docs/](docs/) explain how to set up and use Karakuri. They are
also published at [karakuri.moe/docs.html](https://karakuri.moe/docs.html), and
the same guides ship inside the server and open under **Help** in the web
interface.

### Getting started

- [Installing Karakuri](docs/getting-started.md): Installing with Docker Compose, a release archive, or from source, and using SQLite, PostgreSQL, or MySQL.
- [Home and overview](docs/home.md): The start page, the overview of what needs attention, and pinned dashboards.

### Flows

- [Authoring flow graphs](docs/flow-authoring.md): The flow graph format, port typing, the draft, deploy, and enable lifecycle, triggers, failure handling, and sharing.
- [Scripting](docs/scripts.md): The script node sandbox: what a script reads and returns, what it may reach, and calling nodes from JavaScript, Python, and Bash.
- [AI nodes](docs/ai.md): AI provider connections, the model and agent nodes, tools, memory, and editing flows and dashboards with AI.
- [Agent nodes](docs/agent-nodes.md): Remote agents: enrollment, commands, files, services, and desktop and GUI automation on agent hosts.
- [Browser prompts](docs/browser-prompts.md): Asking a person a question in a browser from a flow and acting on the answer.
- [Network socket nodes](docs/network-sockets.md): TCP, UDP, and WebSocket connections, framing, replies, and limits.

### Dashboards

- [Authoring dashboards](docs/dashboard-authoring.md): Dashboard layout, sections, pages, themes, variables, commands, and widget configuration.
- [What a dashboard exposes](docs/dashboard-security.md): What viewers of a shared or public dashboard can read and operate, renders, and render keys.

### Devices and integrations

- [Home Assistant](docs/homeassistant.md): Areas, floors, devices, and labels from Home Assistant, targeting rooms from flows, and its entities as devices.
- [openHAB](docs/openhab.md): The openHAB connection, items as devices, and the item trigger, command, and read nodes.
- [ESPHome](docs/esphome.md): Connecting ESPHome nodes, their entities as devices, node actions, and limits.
- [Frigate NVR](docs/frigate.md): Frigate cameras on dashboards, detection and review triggers, images, and camera switches.
- [Device auto import](docs/device-auto-import.md): Adding the devices a connection reports automatically, Home Assistant MQTT discovery, filters, and removing devices no longer reported.
- [The state script](docs/mqtt-device-state-script.md): The MQTT device state script: reading one message into the values it carries.
- [The payload script](docs/mqtt-device-payload-script.md): The MQTT device payload script: turning one control change into a topic and payload.
- [Notification services](docs/notifications.md): Notification providers, endpoints, sending from flows, and delivery behavior.
- [Time-series connections and history](docs/timeseries.md): Prometheus, InfluxDB, PostgreSQL, and Home Assistant history queries, history forwarding, and chart data.
- [Energy](docs/energy.md): Energy sources, groups, tariffs and costs, solar, charts, flow nodes, and history import.

### Administration

- [Server configuration](docs/configuration.md): Where the server reads its settings, environment overrides, secrets, and every key.
- [Backup](docs/backup.md): What a backup holds, the schedule, restoring, moving between databases, and permissions.
- [Event log](docs/events.md): What the event log records, actors, retention, permissions, and the event nodes.
- [MCP](docs/mcp.md): Connecting MCP clients, API keys and their limits, the tools, and editor chat.
- [Plugins](docs/plugins.md): Writing plugins: the manifest, nodes, connections, widgets and their scope, and securing plugin routes.

## Things to know

- **Configuration.** The server reads `config.yaml` from the `etc/` directory beside
  the binary, then `~/.config/karakuri/`, then `/etc/karakuri/`. Any key can be
  overridden with a `KARAKURI_` environment variable, such as
  `KARAKURI_SERVER_PORT=9090`. The [server configuration guide](docs/configuration.md)
  lists every key.
- **Ports.** 8080 serves the web interface and API. Agents dial 9443.
- **Updates.** A server or agent running as a service installs newer releases on
  its own once a day and rolls back if the new version will not start. A system
  server install updates from a root timer, so its service account never gets write
  access to root's files. `karakuri update` and `karakuri-agent update` install a
  newer release now.
- **Agents are not sandboxed.** An agent's nodes can do whatever the account it runs
  as can. Run it as the account whose access you mean to hand over.
- **Dashboard renders** need Chrome or Chromium on the server. The Docker image
  includes it.
- **The server's file nodes** reach only the directories in `storage.roots`, which
  default to the `files` directory under the data directory.
- **Monitoring.** `/metrics` serves Prometheus metrics, `/health` answers liveness
  probes, and `/api/docs` is the interactive API reference.
- **Home Assistant Assist.** A custom integration makes a flow an Assist
  conversation agent. Each release has it as `karakuri-assist-<version>.zip`. See
  [homeassistant/README.md](homeassistant/README.md).
- **Desktop automation.** `karakuri-auto` is a standalone command-line tool for the
  local keyboard, mouse, screen, windows, and clipboard, released as its own archive.
  `karakuri-auto describe` shows what it can do on the current machine.

## Building from source

Requires Go (see `go.mod` for the minimum version) and Node.js with npm. Every build
and test goes through the Makefile.

### Development

`make dev` builds a development version for working on Karakuri itself. The
frontend and widgets keep Svelte's runtime checks and source maps, and
`build/etc/config.yaml` links to a development config that logs at debug level to
the console, accepts requests from any origin, and keeps editor chat traces in
`build/var/assistant-traces`.

```sh
make dev                          # everything
make dev TARGET=server            # server only
make dev TARGET=frontend          # frontend, and the server that embeds it
make dev TARGET=agent             # agent only; GOOS and GOARCH cross-compile it
make dev TARGET=auto              # karakuri-auto only
./build/karakuri user create admin admin@example.com <password> --admin
```

On Linux, a full or server build installs the user unit `karakuri-dev.service`.
Start or restart the development server with
`systemctl --user restart karakuri-dev.service`. Elsewhere, run
`./build/karakuri serve`. The web interface is compiled into the server binary, so a
frontend change needs a restart to show.

### Production

```sh
make                                # optimized build of everything into build/
sudo make install                   # runs install.sh on build/, as for a release
sudo karakuri service install
sudo karakuri service start
```

`karakuri service` also takes `stop`, `restart`, `status`, and `uninstall`, and
works with systemd, OpenRC, SysV, launchd, and the Windows service manager. Root
installs a system service that runs as the `karakuri` account, and anyone else a
service of their own. `--scope` and `--user` override both.

### Tests

```sh
make test                        # every unit-test group
make test TARGET=server          # server, shared packages, plugins, and Home Assistant
make test TARGET=frontend        # Svelte type and consistency checks
make test TARGET=agent           # agent command and node packages
make test TARGET=automation      # automation command and standalone module
make test-integration            # boots the server and runs a flow over HTTP
```

`make test` needs no services or installed files.

### Docker images

```sh
make docker                # server image from the checkout, for this machine
make docker-agent          # agent image from the checkout, for this machine
make docker-release        # grmrgecko/karakuri from the archives in dist/
make docker-agent-release  # grmrgecko/karakuri-agent from the archives in dist/
make docker-push           # push the server image
make docker-agent-push     # push the agent image
```

The release images cover `linux/amd64` and `linux/arm64` in one tag. Building them
needs `docker buildx`, the containerd image store, and binfmt emulation for the
architecture that is not the host's (on Arch, the `docker-buildx` and
`qemu-user-static-binfmt` packages). `DOCKER_PLATFORMS=linux/amd64` builds one.
`DASHBOARD_RENDERS=0` leaves Chromium out of the server image, which shrinks it
from 1.5 GB to 450 MB.

Builds, release archives, and images include the 26 MB CPython module Python
scripts run on. Set `PYTHON_SCRIPTS=0` on any of these `make` targets to leave it
out; JavaScript and Bash scripts work without it.

### Website

`website/` is the karakuri.moe site, plain HTML, CSS, and JavaScript.
`make website` renders the guides in `docs/` into `website/docs.html` with the
template in `tools/website-docs/`; run it before publishing the directory. The
default `make` does not build it.

### Releases

```sh
make snapshot   # every archive into dist/, publishing nothing
make release    # publish the tagged release; needs .release-env with GITHUB_TOKEN
```

Both cross-compile in a container, so Docker is the only toolchain they need beyond
Go and Node.js. `make release` refuses unless HEAD carries a tag matching the
`VERSION` file and the working tree is clean.

The macOS binaries are signed with a Developer ID and notarized during the build,
from Linux, when `.signing/` holds the credentials. Git and Docker ignore it:

| File | What it is |
| --- | --- |
| `developer-id.p12` | the Developer ID Application certificate and its private key |
| `developer-id.p12.password` | the password the `.p12` was exported with |
| `notary-api-key.json` | an App Store Connect API key, for notarization |

Export the certificate with its key from Keychain Access; a `.p12` made with
OpenSSL 3 needs `-legacy`. Create the API key in App Store Connect under **Users
and Access > Integrations** with the Developer role, then encode it:

```sh
docker run --rm -v "$PWD:/src" -w /src --entrypoint rcodesign karakuri-cross:v1.26.5 \
    encode-app-store-connect-api-key -o .signing/notary-api-key.json \
    <issuer ID> <key ID> AuthKey_<key ID>.p8
```

Without the certificate the binaries are left as Go builds them. With it but no
API key they are signed only. Notarization waits on Apple for every macOS binary;
`MACOS_NOTARIZE=0` skips it for a snapshot.

## Specifications

[specs/README.md](specs/README.md) indexes the requirements and design plans. The
standalone automation library keeps its own in
[automation/specs/](automation/specs/README.md).
[specs/spec-kit.md](specs/spec-kit.md) describes the workflow and the Codex and
Claude Code skills.
