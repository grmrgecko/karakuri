# Quickstart: Backup and restore

Run against the development server, from `build/`. The passphrase for these
checks is a throwaway string kept in a scratch file, never committed.

## 1. Create and verify while running

1. Record the summary counts from MCP (`list_flows`, `list_dashboards`,
   `list_connections`, `list_mqtt_devices`, `list_controls`).
2. Start a flow that writes a control every second and leave a dashboard
   showing it open.
3. `./karakuri backup create --output $S/backups --passphrase-file $S/pass`.
   The flow keeps running and the dashboard keeps updating (SC-004).
4. `./karakuri backup verify` on the archive: counts match step 1, `ok`.
5. Verify with a wrong passphrase, and after changing one byte in the
   middle of a copy: both refuse (SC-005).

## 2. Restore into a separate installation

1. Make a second installation: copy the `karakuri` binary into
   `$S/inst2/`, with `$S/inst2/etc/config.yaml` from `config gen --env dev`
   (its own port, a new encryption key) and an empty `$S/inst2/var/`. The
   binary finds both beside itself.
2. `$S/inst2/karakuri backup restore` the archive: counts printed, config
   secrets set, the old key saved beside the config.
3. `$S/inst2/karakuri user list` shows the users, and
   `$S/inst2/karakuri config rotate-key --dry-run` opens every stored secret
   with the restored key, which proves the connections' credentials decrypt
   (SC-001). The restored server itself is started in scenario 5 through the
   dev unit, since AGENTS.md keeps `serve` under systemd.
4. Restore again without `--replace`: refused. With `--replace`: a safety
   archive appears beside the input.
5. Restore into the dev installation while `karakuri-dev.service` runs:
   refused naming the lock.
6. Remove `$S/inst2`.

## 3. Engines

On a disposable PostgreSQL container, restore the archive into a config
pointing at it and compare `counts` (SC-003). Remove the container.

## 4. Schedule

1. Set the schedule to every minute, keep 3, in `$S/backups`.
2. After five runs the directory holds 3 archives and the runs list shows 5
   (SC-006).
3. Point the directory at a read-only path: saving the schedule refuses.
4. A flow with `backup-result` on `failed` and a debug node fires when the
   directory is removed under a running schedule.
5. Stop the server across a scheduled minute and start it: one catch-up run.

## 5. Agent reconnection

The dev database, identities, and key are irreplaceable. With the server
stopped, copy `build/var` and `build/etc` aside first, independent of the
feature under test. Then, with an agent enrolled against the dev server, restore a fresh archive over
the dev installation with `--replace` (server stopped), start it, and confirm
the agent reconnects without enrolling (SC-002). The `--replace` safety
archive is the rollback.

## 6. Browser

Settings, Backup: set the schedule, back up now, verify, download, delete.
Screenshots after each step, and a console check.

## Cleanup

Disable the schedule, remove `$S/backups`, the second installation, the test
flow and dashboard, and the passphrase file. Report what was created and
removed.
