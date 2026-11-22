# Feature specification: Backup and restore

**Feature**: `specs/036-backup-restore`
**Created**: 2026-11-05
**Status**: Implemented
**Input**: Back up and restore a whole installation. The database, the
encryption key that protects saved credentials, the JWT secret, the agent CA
and server identity, widget uploads, flow SQLite databases, flow files, and
buffered history are not regenerable; losing any of them loses automations,
devices, dashboards, credentials, or enrolled agents. Create a backup on demand and on a
schedule with retention, as one archive protected by a passphrase because it
holds the keys to every saved credential; verify an archive without restoring
it; restore onto the same or a new machine, including from SQLite to
PostgreSQL or MySQL; download and list backups from settings and the API.

## User scenarios and testing

### User story 1: Back up before something goes wrong (Priority: P1)

An operator about to upgrade karakuri, or replace the disk it runs on, runs one
command or presses one button and gets a single file holding everything the
installation needs to come back: flows, dashboards, controls, devices,
connections with their credentials, energy history, users, enrolled agents,
uploaded images, and the list of external plugins it ran.

**Why this priority**: The encryption key and the agent CA cannot be
regenerated, and the database holds months of authoring. Nothing protects
them today.
**Independent test**: Create a backup of a populated installation, list what it
holds, and verify it, while the server keeps running and serving requests.

**Acceptance scenarios**:

1. **Given** a running installation, **When** the operator creates a backup,
   **Then** one archive file is written holding a consistent copy of the
   database, the secrets section of the configuration, the agent CA and server
   identity, widget uploads, flow SQLite databases, flow files, and buffered
   history, with the external plugins it runs listed by name and version, and
   the server keeps running throughout.
2. **Given** a backup is created, **When** it is written, **Then** it is
   encrypted with the operator's passphrase, and opening it with any other
   passphrase fails without revealing its contents.
3. **Given** an archive, **When** the operator verifies it, **Then** they are
   told the karakuri version, creation time, database engine, record counts per
   kind (flows, dashboards, connections, devices, controls, users, agents), and
   whether every part's checksum matches.
4. **Given** an archive damaged in transit, **When** it is verified, **Then**
   verification names the part that does not match and says the archive cannot
   be restored.

---

### User story 2: Restore onto a new machine (Priority: P1)

The server's disk failed. The operator installs karakuri on a new machine,
restores the last backup, and starts the server: every flow, dashboard,
connection, device, and user is back, saved credentials decrypt, and enrolled
agents reconnect without re-enrolling.

**Why this priority**: A backup that cannot be restored is not a backup.
**Independent test**: Restore an archive into an empty installation in a
separate directory, start it, and compare its records, a decrypted connection,
and an agent's reconnection with the original.

**Acceptance scenarios**:

1. **Given** an archive and an installation that has never run, **When** the
   operator restores it, **Then** the database, secrets, agent identity,
   uploads, flow files, and buffered history are put in place, and the server
   starts with all of it. External plugins the archive lists that the new
   machine lacks are named so the operator can install them.
2. **Given** an installation that already holds data, **When** the operator
   restores without saying to replace it, **Then** the restore is refused and
   nothing is changed. With the replace option, the current state is first
   saved as a backup beside the archive, then replaced.
3. **Given** a running server, **When** the operator tries to restore into its
   installation, **Then** the restore is refused until the server is stopped.
4. **Given** an archive from an older karakuri version, **When** it is
   restored, **Then** the database is upgraded on start as any upgrade would be.
   An archive from a newer version is refused with both version numbers named.
5. **Given** an archive made on SQLite, **When** it is restored into an
   installation configured for PostgreSQL or MySQL, **Then** every record is
   loaded into that database, so a backup also moves an installation between
   database engines.

---

### User story 3: Backups on a schedule (Priority: P2)

The operator sets nightly backups to a directory on another disk, keeping the
last 14, and settings show when the last one ran, its size, and whether it
succeeded. A flow can start a backup too, for example before a risky change.

**Why this priority**: A backup taken once is out of date the next day.
**Independent test**: Set a short schedule, let it run several times, and check
that old archives are pruned to the retention count and the status shows each
result.

**Acceptance scenarios**:

1. **Given** a schedule and a directory, **When** the schedule fires, **Then**
   a backup is written there named with the installation and time, and archives
   beyond the retention count are deleted, oldest first.
2. **Given** a backup that fails, such as a full disk, **When** the schedule
   fires, **Then** the partial file is removed, the failure is shown in
   settings and logged, the previous archives stay, and a Backup Failed trigger
   lets a flow notify someone.
3. **Given** a server down at the scheduled time, **When** it starts, **Then**
   a missed nightly backup runs once rather than waiting another day.

---

### User story 4: Back up and download from settings (Priority: P2)

An administrator opens **Settings, Backup**, sees the schedule and the list of
archives with their sizes and results, creates one now, and downloads it, or
runs the same actions over the API and MCP.

**Why this priority**: Not every operator has a shell on the server.
**Independent test**: Create, list, download, and delete a backup through the
browser and through the API.

**Acceptance scenarios**:

1. **Given** an administrator, **When** they create a backup in settings,
   **Then** they enter a passphrase or use the stored schedule passphrase, and
   the new archive appears in the list with its size and verification result.
2. **Given** a user without backup permission, **When** they open the backup
   page or call its API, **Then** they are refused.
3. **Given** an archive in the list, **When** it is downloaded, **Then** the
   file is the archive itself, encrypted, and the download is logged with the
   user who made it.

### Edge cases

- A backup taken while flows run and devices report is consistent: the database
  copy reflects one moment, and writes during the backup land after it. Each
  flow SQLite database is copied the same way, though not at the same instant
  as the main database.
- Rendered dashboard images, assistant traces, and logs are not backed up; they
  are regenerated or are diagnostics. The archive's contents list says so.
- External plugin executables are not copied: they are built for one platform
  and installed beside the binary, and a restore may land on another platform.
  Their settings and state are in the database and are restored.
- Flow file storage roots outside the data directory are the operator's own
  directories and are not copied; the manifest names them.
- A development build's archive carries the version `dev`; restoring it is
  allowed onto any version with a warning, since no order between them is
  known.
- The configuration file is not restored wholesale: only its secrets section
  (encryption key, JWT secret) is carried, so a new machine keeps its own
  addresses, ports, and paths. The restore shows which settings it set.
- A restore into an installation whose configuration already has a different
  encryption key replaces the key with the archive's, after saving the old
  one beside it, since the archive's credentials need the archive's key.
- A forgotten passphrase cannot be recovered; the archive is unreadable without
  it. Settings say so where the passphrase is entered.
- A scheduled backup's passphrase is stored encrypted with the installation's
  encryption key, so a scheduled backup needs no one present.
- Two backups at once, scheduled and manual, run one after the other.
- A backup directory that is not writable is reported when the schedule is
  saved, not first at night.
- Archives are written to a temporary name and renamed when complete, so a
  listing never shows a half-written archive.
- Large installations: history spools and energy history can be hundreds of
  megabytes; the archive streams and does not hold the whole installation in
  memory.

## Requirements

### Functional requirements

- **FR-001**: The system MUST create a backup as one archive holding a
  consistent copy of the database, the configuration's encryption key and JWT
  secret, the agent CA and server identity, widget uploads, the flows' isolated
  SQLite databases (each copied consistently), flow files under the data
  directory, and buffered history, with a manifest of the karakuri version,
  creation time, database engine, record counts, the external plugins in use
  by name and version, and a checksum per part.
- **FR-002**: Archives MUST be encrypted with a passphrase of at least 12
  characters using an authenticated scheme, so a wrong passphrase or a damaged
  archive is detected and nothing partial is restored.
- **FR-003**: Backups MUST be taken while the server runs, without stopping
  flows or refusing requests, and MUST NOT hold the whole installation in
  memory.
- **FR-004**: The system MUST verify an archive without restoring it, reporting
  its manifest and whether every part matches.
- **FR-005**: The system MUST restore an archive into an installation that is
  not running: refused when the installation holds data unless replacing is
  asked for, in which case the current state is first backed up beside the
  archive. Restore MUST put every part in place or leave the installation as it
  was.
- **FR-006**: Restore MUST carry records between SQLite, PostgreSQL, and MySQL
  installations, and MUST refuse an archive made by a newer karakuri version,
  naming both versions.
- **FR-007**: Restore MUST set only the configuration's secrets from the
  archive, keeping the target's other settings, and MUST keep any replaced
  encryption key beside the configuration.
- **FR-008**: The system MUST run backups on a schedule to a directory with a
  retention count, catch up a missed backup once after a start, remove partial
  files on failure, keep the previous archives, and report each result.
- **FR-009**: A flow trigger MUST fire when a backup fails, and a flow node
  MUST start a backup and report its result.
- **FR-010**: Settings MUST offer a Backup page with the schedule, directory,
  retention, stored passphrase, the list of archives with size, time, and
  verification result, and actions to create, verify, download, and delete.
- **FR-011**: The command line MUST offer creating, verifying, listing, and
  restoring backups; REST and MCP MUST offer creating, listing, verifying,
  downloading, and deleting, under a backup permission held by administrators.
  Restore is command-line only, since it needs the server stopped.
- **FR-012**: Downloads and restores MUST be logged with who performed them.

### Key entities

- **Archive**: one file with a manifest and the installation's parts,
  encrypted with a passphrase.
- **Manifest**: karakuri version, creation time, installation name, database
  engine, record counts, and each part's name, size, and checksum.
- **Backup schedule**: time of day or cron expression, directory, retention
  count, and the stored passphrase.
- **Backup result**: time, archive name, size, duration, and success or the
  reason it failed.

## Success criteria

- **SC-001**: A backup of the development installation, restored into an empty
  installation, starts with the same number of flows, dashboards, connections,
  devices, controls, users, and agents, and every saved connection's credentials
  decrypt.
- **SC-002**: An enrolled agent reconnects to a restored server without being
  enrolled again.
- **SC-003**: A backup restored from SQLite into PostgreSQL holds the same
  record counts.
- **SC-004**: A flow keeps running and a dashboard keeps updating while a backup
  is taken.
- **SC-005**: An archive with one changed byte, or opened with a wrong
  passphrase, is refused by verification and restore, and no file of the target
  installation changes.
- **SC-006**: A schedule set to keep 3 archives holds 3 after 5 runs.

## Assumptions and scope

- Backups are files written to a local or mounted directory; uploading to cloud
  storage is out of scope, as a flow or the operator's own tools can copy the
  archive.
- Restore is offline and command-line only. Restoring from the browser into a
  running server is out of scope.
- One installation per archive; merging two installations is out of scope.
- External time-series databases and Home Assistant are other systems with
  their own backups; karakuri backs up only its own state, including the
  history it has buffered but not yet delivered.
- Administrators hold the new backup permission by default; it is granted like
  any other permission.

## References

- [Feature 001: server operation and identity](../001-server-security/spec.md)
- [Feature 009: remote agents](../009-remote-agents/spec.md)
- [Feature 017: isolated SQLite](../017-isolated-sqlite/spec.md)
- [Feature 027: time-series connections](../027-timeseries-connections/spec.md)
- `AGENTS.md` on irreplaceable databases, identities, and keys
