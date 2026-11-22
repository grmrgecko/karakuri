# Feature specification: Server operation and identity

**Feature**: `001-server-security`
**Created**: 2026-10-04
**Status**: Implemented baseline
**Input**: Operate a persistent home automation server with authenticated users,
explicit permissions, protected secrets, and reproducible installs. Let an API key be
limited to a set of permissions written the way roles write them, never granting
more than its user's roles allow, with the limit offered when a key is created.

## User scenarios and testing

### User story 1: Operate an installation (Priority: P1)

An operator installs or upgrades Karakuri and retains the automations and identities
already stored.

**Why this priority**: Persistence is required before the server can safely control a
home.

**Independent test**: Build the server, restart the development unit, and compare stored
records and encrypted connection readability.

**Acceptance scenarios**:

1. **Given** an installed instance with existing state, **When** the executable and
   resources are upgraded, **Then** configuration, database contents, and encryption
   material remain available.
2. **Given** the development service has started, **When** the operator requests its
   HTTP endpoint and status, **Then** the listener answers and the user unit reports
   active.

### User story 2: Authenticate and authorize (Priority: P1)

An administrator grants users or groups the permissions their work requires.

**Why this priority**: Every integration and flow action depends on this boundary.

**Independent test**: Use a session and an API key with restricted roles against
permitted and denied routes.

**Acceptance scenarios**:

1. **Given** a valid username and password, **When** the user signs in, **Then** the
   server issues access and refresh credentials for a session.
2. **Given** a user lacking a route permission, **When** the user calls that route with
   a valid bearer token, **Then** the request is denied despite successful
   authentication.
3. **Given** a user whose roles grant `api:*`, **When** they create an API key limited
   to `api:flows:read`, **Then** the key lists flows and is refused when it writes a
   flow, edits a dashboard, or reaches another user's flow through `api:flows:manage`.
4. **Given** a user whose roles grant only `api:flows:read`, **When** they create a key
   limited to `api:flows:write`, **Then** creation is refused and no key is stored.
5. **Given** a key limited to `api:flows:*`, **When** the user later loses the role
   granting flow writes, **Then** the key can no longer write flows.
6. **Given** a request authenticated by a limited key, **When** it creates another key,
   **Then** the new key must be limited to permissions the first key also holds.

### User story 3: Inspect service health (Priority: P2)

An operator diagnoses availability and failed work without reading secrets.

**Why this priority**: Operational evidence reduces the time an automation stays broken.

**Independent test**: Read health, metrics, and structured logs after a controlled
failed action.

**Acceptance scenarios**:

1. **Given** the server is running, **When** the operator reads health and metrics
   endpoints, **Then** the service reports health and operational counters.
2. **Given** a lifecycle or execution failure occurs, **When** the operator inspects the
   log, **Then** the event has actionable context without plaintext connection secrets.

### Edge cases

- Invalid credentials must not disclose whether a username exists.
- Access and refresh tokens have distinct purposes; a refresh token is not an API bearer
  credential.
- An upgrade must not replace a configured encryption key or CA with freshly generated
  material.
- An API key created before limits existed, or created without one, holds every
  permission its user holds, as it always has.
- A limited key holding no permissions passes no permission check; object access its
  user has without any permission, such as their own profile, still applies.

## Requirements

### Functional requirements

- **FR-001**: The system MUST authenticate passwords with a stored password hash and
  return the same credential failure for unknown users and incorrect passwords.
- **FR-002**: The system MUST issue access and refresh tokens with distinct audiences
  and validate their expiry, signature, intended use, and matching unexpired session
  record. Deleting the session MUST invalidate both token kinds on subsequent requests.
  Each refresh token MUST be exchangeable once. Presenting an exchanged token MUST
  revoke its session, except within a 30-second grace period after that exchange,
  when it MUST return the session's current pair so concurrent tabs stay signed in.
- **FR-003**: The system MUST accept API keys as bearer credentials associated with a
  user; keep verification material protected and reveal recoverable key values only
  through authorized administration.
- **FR-004**: The system MUST resolve permissions from roles assigned directly to users
  and through groups, including registered wildcard permissions.
- **FR-005**: The system MUST enforce route permissions on the server independently of
  navigation or button visibility.
- **FR-006**: The system MUST support SQLite, PostgreSQL, and MySQL configuration,
  persistent migrations, and a bounded connection pool.
- **FR-007**: The system MUST load configuration and resources from supported install
  and user locations. Config lookup MUST prefer the binary's `etc`, then the user's
  config directory, then `/etc/karakuri`; environment overrides MUST outrank file values
  and defaults. An adjacent resource directory MUST be used alone; otherwise resource
  lookup MUST merge user and system directories in priority order. The web interface
  is compiled into the server binary and is not a resource directory; an install or
  upgrade MUST remove a `frontend/` directory an earlier version installed.
- **FR-008**: The system MUST encrypt connection secret storage with the configured
  encryption key and mask secret fields in ordinary reads.
- **FR-009**: The system MUST generate first-install configuration secrets only when
  configuration is absent; preserve existing config and data during upgrades.
- **FR-010**: The system MUST provide service install, uninstall, start, stop, restart,
  and status actions for supported service managers.
- **FR-011**: The system MUST expose liveness, database readiness, Prometheus metrics,
  structured logging, and an optionally authenticated WebSocket event channel with
  server-side subscription gates. The socket MUST recognize access JWTs and render
  credentials, with anonymous dashboard/public subscriptions subject to topic gates;
  API-key bearer support on REST MUST NOT imply API-key socket authentication.
- **FR-012**: The system MUST apply configured HTTP read/write and graceful HTTP shutdown
  timeouts. MCP, editor chat, and synchronous webhook handlers MAY clear the HTTP write
  deadline and use their own operation budgets. With `server.trust_proxy` off, request
  IP checks MUST use the peer address. With it on, forwarding headers MUST replace
  `RemoteAddr` before handlers run, including webhook IP allowlist checks, only when
  the peer is in `server.trusted_proxies`; `X-Forwarded-For` MUST be read from the
  right, skipping trusted hops.
- **FR-013**: The system MUST throttle login after ten failures on a username or client
  address within ten minutes, reset those keys after a successful login, and keep these
  counters in process memory.
- **FR-014**: The system MUST offer encryption-key rotation that decrypts all connection
  configs, recoverable API keys, CA private keys, and agent credential-store keys before
  transactionally replacing their ciphertext. Unreadable records MUST prevent database
  mutation; dry-run MUST write nothing. Normal rotation MUST stage the config rewrite
  before the database commit and install it afterward, reporting the new key if that
  final install fails. Print-only rotation MUST leave config replacement to the operator.
- **FR-015**: The API key creation form MUST accept an optional expiration date and
  offer In 30 days, In 90 days, In a year, and Never presets. A preset MUST set the
  date field relative to the user's current local date, and Never MUST clear it so
  the key does not expire.
- **FR-016**: The WebSocket hub MUST revalidate every open socket's credential and
  subscriptions at least every 30 seconds and after every successful API or MCP write,
  plugin gate removal, and render job settlement. A socket whose session no longer
  validates MUST become anonymous and be told with `auth_expired`, and MUST be able
  to present a new access token in an `auth` frame. A render socket whose job has
  settled MUST be closed. A subscription that no longer passes its gate or dashboard
  grant MUST be withdrawn and reported with `revoked`.
- **FR-017**: An API key MAY carry a permission limit: up to 64 permission strings in
  the role syntax, wildcards included. A limited key MUST pass a permission check
  only when its user's roles grant the permission and one of the key's strings
  matches it, evaluated on every request so a later role change narrows the key.
  A key without a limit MUST hold its user's permissions.
- **FR-018**: Creating a limited key MUST refuse any string its creator's effective
  permissions do not cover; a creator authenticated by a limited key MUST also be
  covered by that key's limit and MUST NOT create a key without a limit.
- **FR-019**: A key's limit MUST apply to route permissions, to permission checks inside
  flow, dashboard, chart, upload, and event access (including the manage overrides),
  to MCP tool checks, and to permission checks plugin routes make through the SDK.
- **FR-020**: The API key list and creation form MUST show and set a key's limit. The
  form MUST edit the limit with the same permission list a role uses, completing typed
  paths against the registered permissions the creator holds.

### Key entities

- User, group, role, and assignment: identities and the explicit permissions they
  receive.
- Session and API key: JWTs reference a persisted session; API keys hold a verification
  hash, for newly created keys an encrypted recoverable value, an optional permission
  limit, and, for a dashboard render key (feature 007), the dashboard it renders.
- Configuration, database, and resource directory: installation state that survives
  executable replacement.

## Success criteria

- **SC-001**: Restricted-role checks admit every granted operation in the fixture and
  deny its ungranted counterpart.
- **SC-002**: An upgrade fixture retains its database records and decrypts its existing
  connection secrets using the unchanged key.
- **SC-003**: A development build and restart leave the user unit active and the HTTP
  endpoint answering.
- **SC-004**: A key limited to one read permission, owned by an administrator, is
  refused on every write route, MCP write tool, and manage override in the fixture,
  and a request for a permission its user lacks creates no key.

## Assumptions and scope

An open WebSocket keeps its credential and is revalidated every 30 seconds and after
every successful API or MCP write. Session deletion or JWT expiry leaves it anonymous
until it presents a fresh token over the socket, and role, share, or dashboard policy
changes withdraw the subscriptions they no longer allow, as FR-016 states.

Authentication administration is exposed through REST and the CLI, not MCP. This spec
does not promise high availability or database replication.
A refresh token replayed inside the grace period is indistinguishable from a
concurrent tab and is honored.
Database re-encryption and config-file replacement are separate commits; rotation
requires stopping the running server and recovering the reported key if file installation
fails. First-install randomization belongs to the installer and container entrypoint;
`config gen` emits a template with placeholder secrets.
The proxy switch does not restrict forwarding headers to known proxy addresses. Enabling
it makes webhook allowlists depend on the forwarded address; direct untrusted access to
that listener can therefore bypass an IP allowlist. This is an implementation limit,
not a peer-only enforcement guarantee.

## References

- [Design plan](plan.md)
- [System architecture](../architecture.md)
- [README.md](../../README.md)
- [internal/config/server.go](../../internal/config/server.go)
- [pkg/auth/service.go](../../pkg/auth/service.go)
- [pkg/database/database.go](../../pkg/database/database.go)
- [pkg/models/api_key.go](../../pkg/models/api_key.go)
- [pkg/migrate/migrate.go](../../pkg/migrate/migrate.go)
- [pkg/api/router.go](../../pkg/api/router.go)
