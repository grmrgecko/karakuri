# Server configuration

The server reads `config.yaml` from the first of these that exists:

1. `etc/config.yaml` beside the `karakuri` binary
2. `~/.config/karakuri/config.yaml`
3. `/etc/karakuri/config.yaml`

The installer and the Docker image write one with fresh secrets the first time.
Restart the server after editing it.

Any key can also come from an environment variable named `KARAKURI_` plus the
section and key in capitals, joined by underscores. `KARAKURI_SERVER_PORT=9090`
sets `server.port`. An environment variable wins over the file.

## Writing a configuration by hand

`karakuri config gen --env=prod` prints a complete configuration with the defaults
below. Its two secrets, `security.jwt_secret` and `security.encryption_key`, are
placeholders that every generated file shares, so replace both before the server
is reachable by anyone else:

```sh
openssl rand -hex 32   # run once for each secret
```

Set `encryption_key` before saving any connection. Connection credentials are
encrypted with it, so changing it later makes them unreadable.
`karakuri config rotate-key` moves stored credentials to a new key.

## Keys

| Section | Key | Default | Description |
| --- | --- | --- | --- |
| server | host | 0.0.0.0 | Address the web interface and API listen on. |
| server | port | 8080 | Port the web interface and API listen on. |
| server | base_url | http://localhost:8080 | Address people and dashboard renders use to reach the server. |
| server | read_timeout / write_timeout | 30 | HTTP timeouts (seconds). |
| server | shutdown_timeout | 15 | How long a shutdown waits for requests to finish (seconds). |
| server | cors_origins | empty | Other origins allowed to call the API from a browser, separated by commas. |
| server | trust_proxy | false | Believe forwarding headers from the peers in `trusted_proxies`. |
| server | trusted_proxies | 127.0.0.1/32,::1/128 | Reverse proxy addresses or CIDRs, separated by commas. |
| database | type | sqlite | `sqlite`, `postgres`, or `mysql`. |
| database | path | data directory/karakuri.db | SQLite database file. |
| database | dsn | unset | Connection string for PostgreSQL or MySQL. |
| database | max_open_conns / max_idle_conns | 25 / 5 | Database connection pool. |
| database | conn_max_lifetime | 300 | How long one database connection is reused (seconds). |
| security | jwt_secret | generated | Signs sign-in tokens. |
| security | jwt_expiry | 3600 | How long a sign-in token lasts (seconds). |
| security | refresh_expiry | 86400 | How long a session stays signed in without activity (seconds). |
| security | bcrypt_cost | 12 | Password hashing cost. |
| security | encryption_key | generated | Encrypts connection credentials. |
| security | max_upload_size | 1073741824 | Largest file one dashboard widget upload may be (bytes). |
| logging | level | info | `debug`, `info`, `warn`, or `error`. |
| logging | format | json | `json`, or `console` for human-readable output. |
| engine | max_parallel_nodes | 4 | Nodes one run executes at once. A node that is waiting gives its slot up. |
| engine | max_concurrent_runs_per_flow | 10 | Runs one flow may have at once; 0 is unlimited. |
| engine | default_run_timeout | 300 | Time limit for one run, not counting time spent waiting (seconds); 0 is none. |
| script | network | full | What script HTTP requests may reach: `full`, `bogons` (public addresses only), or `none`. |
| script | timeout | 30 | Time limit for one script (seconds). |
| script | max_output | 262144 | Console output kept from one script (bytes). |
| script | max_http_requests | 20 | HTTP requests one script may make. |
| script | max_http_response | 8388608 | Bytes read from one HTTP response. |
| script | memory_limit_mb | 256 | Memory one script may use, where the language allows a limit. |
| script | python_enabled | true | Offer Python in the script node. |
| script | python_wasm | unset | Path to the CPython WebAssembly module, when it is not in the runtimes directory. |
| storage | roots | data directory/files | Directories the server's file nodes may use. An empty list allows none; `["/"]` allows any. |
| storage | max_read_size | 8388608 | Largest file one Read File node reads (bytes). |
| location | latitude / longitude | 0 / 0 | Default position for sunrise and sunset schedules. At 0 and 0, each such node needs its own. |
| render | workers | 2 | Dashboard renders that run at once. |
| render | timeout | 30 | Time limit for one render (seconds). |
| render | max_queue_size | workers × 4 | Renders that may wait; more are refused. |
| render | browser_path | unset | Chrome or Chromium binary, when it is not in the usual places. |
| render | storage_path | data directory/renders | Where rendered images are kept. |
| render | retain | 3600 | How long a one-off render stays on disk (seconds). A scheduled render stays until the next one replaces it. |
| theme | default | catppuccin-mocha | Dashboard theme for users and dashboards that pick none. |
| agents | listen | 0.0.0.0:9443 | Address remote agents connect to. Empty turns agents off. |
| agents | enroll_ttl | 900 | How long a join token stays valid (seconds). |
| agents | cert_ttl | 7776000 | How long an agent's certificate lasts before it renews (seconds). |
| mcp | enabled | true | Serve the Model Context Protocol at `/mcp`. |
| mcp | assistant_trace_dir | unset | Directory for editor chat traces. They hold prompts and tool data, so leave it unset unless you are debugging. |
| update | enabled | true in new production configs, otherwise false | Install newer releases automatically; see below. |
| update | prerelease | false | Also update to releases marked as pre-releases. |
| update | notify | true | When this server does not install releases itself, as in a container, report a newer one in the event log and on the overview. |
| update | allow_downgrade | false | Let a release older than the one that last ran start on the database anyway. |

Dashboard renders open the dashboard at `server.base_url`, so set it to an address
the server itself can reach, not only one that works from your browser.

## Automatic updates

With `update.enabled`, a server running as a service checks GitHub releases once a
day. When a newer release exists, it downloads the archive for its platform, checks
it against the release's checksums, makes sure the new binary runs, and replaces
its binary together with the plugins, themes, and Python runtime beside it. It then
shuts down and its service manager starts the new version. If that version fails to
start three times, the previous files are put back and the old version starts
again. The event log records each update and each failure.

An install under your home directory, or an unpacked archive, updates itself. A
system install made with `sudo ./install.sh` belongs to root while its service runs
as the `karakuri` account, which cannot write those files. For that install,
`sudo karakuri service install` also adds `karakuri-update.timer`, which runs
`karakuri update --scheduled` as root once a day. It does nothing while
`update.enabled` is off, so turning updates on or off needs only the configuration
change. When it installs a release, it restarts the service and watches it for 30
seconds. A version that does not start, or stops in that time, gets the previous
files back and the service is restarted on them. `journalctl -u karakuri-update`
shows what each run did. The timer needs systemd. On OpenRC or SysV, schedule
`karakuri update --scheduled` from root's crontab instead.

`karakuri update` checks and installs now, and `karakuri update --check` only
reports whether a newer release exists. `karakuri update --force` downloads the
latest release and replaces the install with it even when it is not newer, to
repair an install whose binary or plugins were damaged or changed by hand. Run it as the account that owns the
install: yourself for a user install, or `sudo karakuri update` for a system
install. With the service installed, it restarts the service on the new version
the same way the timer does. A development build and the Docker image never update
themselves.

### Every upgrade, however it arrives

These apply whether the new release came from the updater, a package, or a new
container image.

- **The database is copied first.** When a release starts on a database an older
  release last used, it copies the database before changing it. With scheduled
  backups set up, the copy is a full backup archive in the backup directory,
  restorable with `karakuri backup restore` by either release. Without a stored
  backup passphrase, a SQLite database is copied to `pre-update/` in the data
  directory, and the newest three copies are kept; stop the server and put a copy
  in place of the database file to go back. A PostgreSQL or MySQL database is only
  copied through scheduled backups, so set them up if you use one. A release that
  fails and restarts copies the database once, not on every attempt.
- **The change is recorded.** Once the new release serves, the event log reads
  "Updated from X to Y" and names the copy. A copy that could not be made is
  logged as a warning; the upgrade still goes ahead.
- **An older release refuses a newer release's database.** The newer release may
  have changed data the older one cannot read. Run the newer release again, or
  restore the copy taken before the upgrade. Set `update.allow_downgrade` only to
  start the older release on the changed data anyway, accepting what it cannot
  read.
- **A newer release is reported.** A server that does not install releases itself
  checks once a day and reports a newer one in the event log and on the overview,
  while `update.notify` is on.
