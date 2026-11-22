# Feature specification: Automatic updates

**Feature**: `specs/048-automatic-updates`
**Created**: 2026-11-21
**Status**: Implemented 2026-11-21
**Input**: Let the server and the agent install newer releases on their own, as
the account their service runs as, and recover when a new version will not
start. System server installs must update too, without their service account
gaining write access to root's files.

## User scenarios and testing

### User story 1: An agent keeps itself current (Priority: P1)

An agent installed for a user and running as that user's service finds a newer
release, installs it over its own binary, and restarts on it, with nobody logging
in to the host.

**Why this priority**: Agents run on many hosts that are rarely visited.
**Independent test**: Serve a release with server, agent, and automation archives
and checksums, and update an installed agent binary from it.

**Acceptance scenarios**:

1. **Given** a newer release, **When** the agent's daily check runs, **Then** it
   downloads its own archive, verifies the checksum, installs the binary, and exits
   so its service manager starts the new version.
2. **Given** a binary the service's account cannot write, **When** a newer release
   exists, **Then** the agent logs that it cannot update itself and keeps running.

### User story 2: The server keeps itself and its plugins current (Priority: P1)

A server running as a service installs a newer release's binary together with the
plugins, themes, and runtimes beside it, so the plugins always match the server.

**Why this priority**: A server binary updated without its plugins can load
plugins from another release.
**Independent test**: Update an install directory holding a binary and plugins
from a release archive.

**Acceptance scenarios**:

1. **Given** a newer release, **When** the server updates, **Then** the binary and
   each resource directory the archive carries are replaced, and the event log
   records the update.
2. **Given** a development build or a container, **When** the server runs, **Then**
   it never updates itself. A container release still reports a newer release, as
   user story 4 describes.
3. **Given** a system install whose files belong to root and whose service runs as
   `karakuri`, **When** a newer release exists and `update.enabled` is set, **Then**
   a daily root timer installs it, restarts the service, and keeps it once the
   service stays up.

### User story 3: A broken release undoes itself (Priority: P2)

A new version that fails to start is replaced by the version it replaced, without
anyone intervening.

**Why this priority**: An unattended update must not leave a host without its
server or agent.
**Independent test**: Start an updated install that never reports healthy.

**Acceptance scenarios**:

1. **Given** an applied update, **When** the new version starts healthy, **Then**
   the replaced files are removed.
2. **Given** an applied update, **When** the new version fails to report a healthy
   start three times, **Then** the replaced files are put back and the process exits
   for its service manager to start the old version.
3. **Given** an update installed by the root timer, **When** the restarted service
   fails to start or stops within 30 seconds, **Then** the timer puts the replaced
   files back and restarts the service on them.

### User story 4: A container upgrade is safe and visible (Priority: P1)

A server in a container is upgraded by pulling a newer image, by hand or by a tool
such as Watchtower, with no updater of its own. The operator learns that a newer
release exists, the database is copied before the new release changes it, the
change is recorded, and an image put back to an older release does not run on data
it cannot read.

**Why this priority**: An unattended image swap has none of the in-place updater's
checks, and a database migration cannot be undone by changing the image back.
**Independent test**: Start a release on a database last used by an older release,
then by a newer one, and ask the overview about a newer published release.

**Acceptance scenarios**:

1. **Given** a database last used by an older release, **When** a newer release
   starts, **Then** it copies the database before migrating and, once serving,
   records its version and logs "Updated from X to Y" with where the copy went.
2. **Given** a database last used by a newer release, **When** an older release
   starts, **Then** it refuses to start, naming both releases, unless
   `update.allow_downgrade` is set.
3. **Given** a release that does not install updates itself, **When** its daily
   check finds a newer release, **Then** the event log records it once and the
   overview shows it to users who may install plugins until the running release is
   at least as new.
4. **Given** the container image, **When** the server serves, **Then** the
   container's healthcheck reports healthy, and when it stops answering, unhealthy.
5. **Given** a newer release that fails to start, **When** its service manager or
   restart policy starts it again, **Then** the database is not copied again for the
   same upgrade.

### Edge cases

- A release binary that does not run on the host, or reports another version, is
  refused before anything is replaced.
- An update applies only to a process its service manager started, since only a
  managed process comes back after exiting. `karakuri update` and
  `karakuri-agent update` install on demand and ask for a service restart.
- A system install made by root and run as the `karakuri` account cannot update
  itself. Its root timer updates it, and the server's own check stands aside when
  the timer is installed. The service account never writes the files or the
  update record, since a rollback run as root renames the paths that record names.
- An update interrupted while the timer watches the service, by a reboot or a
  stopped run, is settled by the next run: kept if the service runs, undone if not.
- The timer needs systemd; other service managers run `karakuri update
  --scheduled` from root's crontab.
- Pre-releases are ignored unless enabled.
- A database used only by releases from before versions were recorded has no
  record. Nothing is refused or copied for it; the first release that serves
  records itself.
- A development build without a release version is neither recorded nor compared.
- The version is recorded only once the server serves, so a release that never
  comes up does not claim the database, and an install the in-place updater puts
  back still starts.
- Without a stored backup passphrase, a PostgreSQL or MySQL database is not copied
  before an upgrade; the event log says so.

## Requirements

### Functional requirements

- **FR-001**: The server and the agent MUST check GitHub releases at most once a
  day, with jitter, when enabled and started by a service manager.
- **FR-002**: An update MUST select the artifact's own archive for the host's
  platform, verify it against the release's `checksums.txt`, and refuse it when its
  binary does not run or report the release's version.
- **FR-003**: An update MUST replace the binary and, for the server, each of
  `plugins`, `themes`, and `runtimes` the archive carries, keeping the replaced
  files until the new version confirms.
- **FR-004**: An update MUST refuse to start when the running account cannot write
  the binary's directory or the resource directory, and report why.
- **FR-005**: After installing an update the process MUST shut down and exit with
  a non-zero status so its service manager restarts it.
- **FR-006**: A new version MUST confirm the update once healthy: the server when
  it serves HTTP, the agent after a minute running. Three starts without
  confirmation MUST restore the replaced files.
- **FR-007**: The server MUST be configured by `update.enabled` and
  `update.prerelease`, on in production configurations and off in development
  configurations, containers, and when unset. The agent MUST be configured by
  `auto_update` (default on) and `update_prerelease`.
- **FR-008**: `karakuri update` and `karakuri-agent update` MUST install a newer
  release on demand, and report one without installing with `--check`.
  `karakuri update --force` MUST install the latest release even when it is not
  newer than the running one, through the same verification, smoke test, and
  confirmation, and `--scheduled` MUST ignore `--force`.
- **FR-009**: `karakuri service install` for a system service under systemd MUST
  install and enable a root timer that runs `karakuri update --scheduled` daily,
  and `karakuri service uninstall` MUST remove it. `--scheduled` MUST do nothing
  while `update.enabled` is off.
- **FR-010**: `karakuri update` with the service installed MUST restart the service
  on the new version, confirm the update after the service has stayed running for
  30 seconds, and otherwise restore the replaced files and restart the service on
  them. Its update record MUST live in the resource directory it replaces, which
  only the updating account can write.
- **FR-011**: The server MUST record, once it serves, the release version that
  started on the database. A release older than the recorded one MUST refuse to
  start, naming both, unless `update.allow_downgrade` is set, in which case the
  event log MUST record a warning.
- **FR-012**: Before a release migrates a database last used by an older recorded
  release, it MUST copy the database: a verified backup archive stamped with the
  older release, in the backup directory, when a scheduled backup passphrase is
  stored; otherwise, for SQLite, a consistent copy in the data directory's
  `pre-update` folder, keeping the newest three. One copy MUST be taken per
  upgrade, however often the release restarts. A failed or impossible copy MUST
  NOT stop the start; the event log MUST record why.
- **FR-013**: A server that serves on a database last used by an older recorded
  release MUST log "Updated from X to Y" in the event log, however the release was
  installed.
- **FR-014**: A server that does not install releases itself MUST, while
  `update.notify` is on (the default) and its version is a release, check for a
  newer release at most once a day, record the first finding of each release in the
  event log, and report it as an overview attention item to users with
  `api:plugins:write` while it is newer than the running release.
- **FR-015**: The server image MUST declare a healthcheck that runs
  `karakuri healthcheck`, which exits zero only when the server on the host answers
  its health route. The shipped Compose files MUST give the server 45 seconds to
  stop.

### Key entities

- **Pending update**: the versions, the replaced files and their backups, and the
  failed start count, stored in the server's data directory or the agent's
  identity directory.
- **Recorded version**: the release that last served on the database, the
  `server.version` setting.
- **Available release**: the newest release a check found and this server has not
  installed, the `update.available` setting.
- **Pre-update copy**: the archive or SQLite copy taken before an upgrade, and the
  `pre-update-backup.json` marker naming the upgrade it belongs to.

## Success criteria

- **SC-001**: An agent and a server update from a release laid out as published,
  each taking only its own archive.
- **SC-002**: A version that never confirms is replaced by its predecessor on the
  fourth start.
- **SC-004**: A root-run update whose service fails to start leaves the previous
  binary and plugins in place and the service restarted on them.
- **SC-003**: `make test` and `make dev` pass and the development service stays
  active.
- **SC-005**: A start on a database recorded by an older release leaves one copy
  that holds the older release's data, across two starts; a start on one recorded
  by a newer release fails without changing the database.
- **SC-006**: A container from the image reports healthy within its start period.

## Assumptions and scope

- Releases are published on GitHub at `grmrgecko/karakuri` with the archive names
  and `checksums.txt` the release configuration produces.
- Agents and servers update independently, each to the newest release.

## References

- [Server security and install](../001-server-security/spec.md)
- [Remote agents](../009-remote-agents/spec.md)
- [Plugins](../008-plugin-system/spec.md)
