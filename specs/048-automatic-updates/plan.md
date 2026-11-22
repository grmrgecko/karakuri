# Design plan: Automatic updates

**Feature**: `specs/048-automatic-updates` | **Date**: 2026-11-21 | **Spec**: [spec.md](spec.md)

## Summary

`internal/update` finds, verifies, and installs releases for both binaries, using
`github.com/creativeprojects/go-selfupdate` for release detection, asset download,
checksum validation, and the binary swap. The server and agent call it from a daily
loop when a service manager started them, and from an `update` command. After
installing, the process exits with status 3 so the manager restarts it; the
restarted process confirms the update or, after three failed starts, restores the
previous files.

## Design

- **Detection.** `Check` lists releases through a `selfupdate.Source` (GitHub; a
  package variable lets tests serve releases from memory), filtered to the
  artifact's archive for the host, `<name>-<version>.<os>-<arch>.tar.gz` or
  `.zip`, with 32-bit ARM as `armv7`. go-selfupdate does no platform matching
  once a filter is given, so the filter names the platform; the digit after the
  name keeps the server, agent, and automation archives apart.
  `TestPlatformFilterPicksTheHostsArchive` runs it against the v0.1.0 release's
  asset names, where the macOS server archive comes first.
  Pre-releases are skipped unless enabled. A release is offered only when its
  version is greater than the running one, unless `Options.Force`, which
  `karakuri update --force` sets to reinstall the latest release over a damaged
  install. The timer's `--scheduled` run never forces.
- **Apply.** `Writable` checks the binary's directory and, for the server, the
  resource root (the directory holding the plugins in use) by creating a temporary
  file. `Apply` downloads the archive and `checksums.txt`, validates, extracts into
  a staging directory beside the install (so moves are renames on one
  filesystem), dropping the archive's wrapping directory and refusing entries that
  leave it, runs `<new binary> version` and expects the release's version, then
  renames each carried resource directory into place with the old one kept as a
  dot-file backup, and swaps the binary with `update.Apply`, which keeps the old
  binary as `.karakuri.old`. A failure part way restores what was moved.
- **Pending record.** `update-pending.json` in the server's data directory or the
  agent's identity directory holds the versions, paths, backups, and attempts.
  `Recover` runs at start: for the version it names it counts the start and, past
  three, restores the backups and reports a rollback, on which the process exits
  with status 3; a record for another version is dropped. `Confirm` removes the
  backups. The server confirms after `NotifyReady`; the agent after a minute.
- **Restart.** Status 3 restarts the process under every manager the service
  definitions configure: systemd `Restart=always`, launchd `RunAtLoad` and keep
  alive, the Windows service manager's restart on failure, and the logon task's
  restart on failure. The server runs its normal shutdown first.
- **Configuration.** Server `update.enabled` defaults false; the production
  template writes true and the development template false. Both Dockerfiles set
  `KARAKURI_UPDATE_ENABLED=false`, and the agent image's `agent.yaml` sets
  `auto_update: false`. The agent's `auto_update` defaults true.
- **Installs.** Updating needs the updating account to own the files. A user
  install of the agent, or of the server, updates in process. A root system install
  whose service runs as `karakuri` must not let that account write root-owned
  binaries, or a compromised service could plant code root runs. Instead,
  `karakuri service install` for a system service under systemd writes
  `karakuri-update.service` (oneshot) and `karakuri-update.timer` (daily, up to 4h
  randomized delay, persistent) to `/etc/systemd/system` and enables the timer;
  uninstall removes them. The unit runs `karakuri update --scheduled` as root,
  which returns at once while `update.enabled` is off. The server's own loop skips
  when the install is not writable and the timer exists.
- **Supervised update.** `karakuri update` with the service installed calls
  `update.Supervise`: `Apply`, then `Restart` through a `service.Handle`. Under
  systemd the server unit is `Type=notify`, so the restart returns only once the
  new version is serving and fails if it exits first. After 30 seconds the handle
  must still report running, and the update is confirmed; otherwise the backups
  are restored and the service restarted. The record is written to the resource
  root instead of the data directory, because the data directory belongs to the
  service account and the record names the paths a root rollback renames.
  `update.Resume` runs first on each invocation and settles a record left by an
  interrupted run: confirm if the service runs, roll back if not. The resource
  root is chosen from where an install puts its resources (beside the binary, the
  user data directory, the system resource directory), never the configuration
  directories, whose plugins are a person's own.

- **Version record and downgrade guard.** `migrate.StoredVersion` reads the
  `server.version` setting before migrations, when only the settings table can be
  relied on, and `prepareUpgrade` in `cmd/karakuri/upgrade.go` runs between
  `db.Init` and `migrate.AutoMigrate` for every command that opens the database.
  Versions compare with `Masterminds/semver`; a non-release version compares with
  nothing. An older binary returns an error unless `update.allow_downgrade`. The
  server calls `recordStart` once it serves, which stores the version and writes
  the event log entries the start collected, since the event log is not running
  when the check happens. The in-place updater's own "Updated" entry moved here so
  every install reports the change once.
- **Pre-update copy.** `preUpdateBackup` writes a full archive with `backup.Write`
  and checks it with `backup.Verify`, stamped with the older release so either
  release restores it, when `backup.StoredPassphrase` answers. The archive is
  written under a `.partial` name and renamed once verified, and no `backup_runs`
  row is written, since the table may not have the current columns yet. The dump
  reads only the tables and columns the database has, which makes an archive of the
  older schema look like an older release's, which restore already accepts. Without
  a passphrase a SQLite database is copied with `VACUUM INTO` into
  `<data>/pre-update`, keeping three. PostgreSQL and MySQL images carry no dump
  tool, so they are copied only through the archive. `pre-update-backup.json`
  names the upgrade a copy belongs to, so a crash-looping release copies once.
- **Notices.** With `update.enabled` off or no service manager, the server runs
  `update.Loop` with `notifyUpdate`, which calls `Check` and stores a newer
  release in the `update.available` setting, recording an event the first time
  each release is seen. An install that cannot write its files and has no timer
  does the same. `homeops.updateItems` reports the setting as an attention item
  under `api:plugins:write` while it is newer than the running release, so an
  upgraded container stops showing it without another check.
- **Containers.** `karakuri healthcheck` loads the configuration and requests
  `/api/health` on the configured host, or loopback for a wildcard host. Both
  Dockerfiles declare `HEALTHCHECK` with a five-minute start period for migrations
  and the pre-update copy. The Compose files set `stop_grace_period: 45s`, which
  covers the HTTP drain (15 seconds), plugin stop (10), and run suspension (5).

## Constitution check

- **Existing state.** Two settings rows are added, `server.version` and
  `update.available`. A database without them is treated as unknown, so existing
  installations start unchanged and record themselves on the first start. The
  guard refuses only a release older than a recorded one, and
  `update.allow_downgrade` overrides it. The pending record and backups live beside
  existing state and are removed on confirmation.
- **Failures.** Detection, download, verification, smoke test, and swap failures
  leave the running version untouched and are logged; the server also records
  them in the event log.

## Verification

- `make test TARGET=server PKGS=./internal/update/`: archive extraction and
  traversal refusal, rollback after three starts, confirmation, and agent and
  server updates from an in-memory release laid out as published, supervised
  updates that keep a healthy version or restore a failed one, and resuming an
  interrupted supervised update.
- `make test TARGET=server`: `TestStartRefusesOlderAndCopiesBeforeNewer` for the
  guard, the override, and one copy per upgrade read back from disk;
  `TestDumpReadsAnOlderSchema` for archives of an older schema.
- Live: set `server.version` on a stopped development database to an older and
  then a newer release, start the service, and check the copy, the event, and the
  refusal. Set `update.available` and read `get_attention`. Build the image, run
  it, and wait for `healthy`.
- `systemd-analyze verify` on the rendered timer and unit.
- `karakuri update --check` and `karakuri-agent update --check` against GitHub.
