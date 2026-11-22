package config

import "fmt"

func devConfig(jwtSecret, encryptionKey string) string {
	return fmt.Sprintf(`server:
  host: "127.0.0.1"
  port: 8080
  base_url: "http://localhost:8080"
  read_timeout: 30
  write_timeout: 30
  shutdown_timeout: 15
  cors_origins: "*"
  trust_proxy: false
  # Peers whose X-Forwarded-For and X-Real-IP are believed when trust_proxy is
  # on, as IPs or CIDRs separated by commas. Add your reverse proxy's address.
  trusted_proxies: "127.0.0.1/32,::1/128"

database:
  type: "sqlite"
  max_open_conns: 5
  max_idle_conns: 2
  conn_max_lifetime: 300

# These are shared placeholder secrets, identical in every generated config.
# Replace both before exposing this instance. Generate values with:
#   openssl rand -hex 32
# Changing encryption_key after connections are saved makes their stored
# credentials undecryptable, so set it before creating any connection.
security:
  jwt_secret: "%s"
  jwt_expiry: 3600
  refresh_expiry: 86400
  bcrypt_cost: 12
  encryption_key: "%s"
  max_upload_size: 1073741824

logging:
  level: "debug"
  format: "console"

engine:
  max_parallel_nodes: 4
  max_concurrent_runs_per_flow: 10
  default_run_timeout: 300

# Sandboxed script node. Scripts run in interpreters with no filesystem,
# process, or environment access; network is the only capability granted and
# this policy decides how much of it:
#   full   - any destination the host can reach (default)
#   bogons - public addresses only; loopback, private, and reserved ranges are
#            refused, which keeps scripts away from the LAN, the host's own
#            services, and cloud metadata endpoints
#   none   - no outbound requests at all
# Python needs the CPython WebAssembly module installed (make runtimes).
script:
  network: "full"
  timeout: 30
  max_output: 262144
  max_http_requests: 20
  max_http_response: 8388608
  memory_limit_mb: 256
  python_enabled: true
  python_wasm: ""

# File nodes (Read File, Write File, Watch Files). They run as the service
# account, so roots confines them and refuses anything outside the list. Left
# commented out, the nodes are confined to the files directory under this
# install's data directory. An empty list turns them off, and ["/"] lifts the
# confinement. Paths are checked after symlinks are resolved, so a link inside
# an allowed root cannot lead out of it. To reach the rest of a machine, run an
# agent there as a user that already has the access you want.
# max_read_size caps a single Read File in bytes.
storage:
  # roots:
  #   - /srv/karakuri/files
  max_read_size: 8388608

# Default geographic position for schedule nodes that trigger on sun events
# (sunrise, sunset, dusk). Nodes may override it; leaving both at 0 means every
# such node must carry its own coordinates.
location:
  latitude: 0.0
  longitude: 0.0
# Remote agents dial this port over mTLS and their nodes then appear in the
# flow editor. Enrolling takes a join token minted in Settings > Agents, which
# carries this server's CA so the agent verifies what it dials with nothing
# installed first; no address is named here or in any certificate, so an agent
# reaches this server by whatever address works. Empty turns the subsystem off.
# enroll_ttl bounds how long a minted token is accepted, cert_ttl how long an
# issued certificate lives — the agents' and the listener's own, both reissued
# automatically at a third of that remaining. Both are seconds.
agents:
  listen: "0.0.0.0:9443"
  enroll_ttl: 900
  cert_ttl: 7776000

# The MCP endpoint at /mcp lets an AI agent operate this instance over the
# Model Context Protocol: flows, runs, controls, connections, MQTT devices,
# dashboards, and plugins. It authenticates with the same bearer credentials
# as the REST API (a session token or an API key) and checks the same api:*
# permission per tool, behind one extra gate: only a user granted api:mcp:use
# through a role may use the endpoint at all. Admins hold it via api:*.
mcp:
  enabled: true
  # Optional private turn traces contain prompts, tool data, and provider responses.
  assistant_trace_dir: ""

# Automatic updates replace the server with newer GitHub releases. Off for a
# development build, which must keep the code it was built from. notify
# reports a newer release without installing it, and allow_downgrade lets an
# older release start on a database a newer one has already used.
update:
  enabled: false
  prerelease: false
  notify: true
  allow_downgrade: false

# Dashboard renders: a headless browser opens a dashboard and photographs it,
# which is what makes one embeddable, mailable, and displayable on a frame that
# speaks nothing but HTTP. It needs Chrome or Chromium installed; browser_path
# names the binary when it is somewhere unusual. workers is how many renders
# run at once, timeout bounds one page in seconds, and retain is how long an
# ad-hoc render stays on disk. A scheduled render is kept until the next one of
# that schedule replaces it. The browser reaches this server at server.base_url,
# so that has to be an address the host itself can dial.
render:
  workers: 2
  timeout: 30
  retain: 3600
  browser_path: ""
  # storage_path: "/var/lib/karakuri/renders"

# Dashboard theme applied when neither the user nor the dashboard picks one.
# Names a theme package directory under themes/.
theme:
  default: "catppuccin-mocha"
`, jwtSecret, encryptionKey)
}

func prodConfig(jwtSecret, encryptionKey string) string {
	return fmt.Sprintf(`server:
  host: "0.0.0.0"
  port: 8080
  base_url: "http://localhost:8080"
  read_timeout: 30
  write_timeout: 30
  shutdown_timeout: 15
  cors_origins: ""
  trust_proxy: false
  # Peers whose X-Forwarded-For and X-Real-IP are believed when trust_proxy is
  # on, as IPs or CIDRs separated by commas. Add your reverse proxy's address.
  trusted_proxies: "127.0.0.1/32,::1/128"

database:
  type: "sqlite"
  max_open_conns: 25
  max_idle_conns: 5
  conn_max_lifetime: 300

# These are shared placeholder secrets, identical in every generated config.
# Replace both before exposing this instance. Generate values with:
#   openssl rand -hex 32
# Changing encryption_key after connections are saved makes their stored
# credentials undecryptable, so set it before creating any connection.
security:
  jwt_secret: "%s"
  jwt_expiry: 3600
  refresh_expiry: 86400
  bcrypt_cost: 12
  encryption_key: "%s"
  max_upload_size: 1073741824

logging:
  level: "info"
  format: "json"

engine:
  max_parallel_nodes: 4
  max_concurrent_runs_per_flow: 10
  default_run_timeout: 300

# Sandboxed script node. Scripts run in interpreters with no filesystem,
# process, or environment access; network is the only capability granted and
# this policy decides how much of it:
#   full   - any destination the host can reach (default)
#   bogons - public addresses only; loopback, private, and reserved ranges are
#            refused, which keeps scripts away from the LAN, the host's own
#            services, and cloud metadata endpoints
#   none   - no outbound requests at all
# Python needs the CPython WebAssembly module installed (make runtimes).
script:
  network: "full"
  timeout: 30
  max_output: 262144
  max_http_requests: 20
  max_http_response: 8388608
  memory_limit_mb: 256
  python_enabled: true
  python_wasm: ""

# File nodes (Read File, Write File, Watch Files). They run as the service
# account, so roots confines them and refuses anything outside the list. Left
# commented out, the nodes are confined to the files directory under this
# install's data directory. An empty list turns them off, and ["/"] lifts the
# confinement. Paths are checked after symlinks are resolved, so a link inside
# an allowed root cannot lead out of it. To reach the rest of a machine, run an
# agent there as a user that already has the access you want.
# max_read_size caps a single Read File in bytes.
storage:
  # roots:
  #   - /srv/karakuri/files
  max_read_size: 8388608

# Default geographic position for schedule nodes that trigger on sun events
# (sunrise, sunset, dusk). Nodes may override it; leaving both at 0 means every
# such node must carry its own coordinates.
location:
  latitude: 0.0
  longitude: 0.0
# Remote agents dial this port over mTLS and their nodes then appear in the
# flow editor. Enrolling takes a join token minted in Settings > Agents, which
# carries this server's CA so the agent verifies what it dials with nothing
# installed first; no address is named here or in any certificate, so an agent
# reaches this server by whatever address works. Empty turns the subsystem off.
# enroll_ttl bounds how long a minted token is accepted, cert_ttl how long an
# issued certificate lives — the agents' and the listener's own, both reissued
# automatically at a third of that remaining. Both are seconds.
agents:
  listen: "0.0.0.0:9443"
  enroll_ttl: 900
  cert_ttl: 7776000

# The MCP endpoint at /mcp lets an AI agent operate this instance over the
# Model Context Protocol: flows, runs, controls, connections, MQTT devices,
# dashboards, and plugins. It authenticates with the same bearer credentials
# as the REST API (a session token or an API key) and checks the same api:*
# permission per tool, behind one extra gate: only a user granted api:mcp:use
# through a role may use the endpoint at all. Admins hold it via api:*.
mcp:
  enabled: true
  # Optional private turn traces contain prompts, tool data, and provider responses.
  assistant_trace_dir: ""

# Automatic updates: a server running as a service checks GitHub releases once
# a day and installs a newer one over its own files, when the account it runs
# as can write them, then restarts through its service manager. A new version
# that fails to start three times is replaced by the previous one again.
# prerelease also accepts releases marked as pre-releases. notify reports a
# newer release in the event log and on the overview when this server does not
# install it itself, as in a container. A release older than the one that last
# ran refuses to start on the database unless allow_downgrade is set, since
# the newer release may have changed data the older one cannot read.
update:
  enabled: true
  prerelease: false
  notify: true
  allow_downgrade: false

# Dashboard renders: a headless browser opens a dashboard and photographs it,
# which is what makes one embeddable, mailable, and displayable on a frame that
# speaks nothing but HTTP. It needs Chrome or Chromium installed; browser_path
# names the binary when it is somewhere unusual. workers is how many renders
# run at once, timeout bounds one page in seconds, and retain is how long an
# ad-hoc render stays on disk. A scheduled render is kept until the next one of
# that schedule replaces it. The browser reaches this server at server.base_url,
# so that has to be an address the host itself can dial.
render:
  workers: 2
  timeout: 30
  retain: 3600
  browser_path: ""
  # storage_path: "/var/lib/karakuri/renders"

# Dashboard theme applied when neither the user nor the dashboard picks one.
# Names a theme package directory under themes/.
theme:
  default: "catppuccin-mocha"
`, jwtSecret, encryptionKey)
}
