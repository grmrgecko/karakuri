# Design plan: Server operation and identity

**Feature**: `001-server-security` | **Date**: 2026-10-04 | **Spec**: [spec.md](spec.md)
**Status**: Implemented baseline

## Summary

Operate a persistent home automation server with authenticated users, explicit
permissions, protected secrets, and reproducible installs.

This plan records the design supporting the baseline requirements. It is not
a pending implementation queue. Requirement IDs are local to this feature.

## Technical context and design

The Go server composes configuration, database migrations, authentication,
permissions, API routing, event delivery, integrations, and the execution engine
in `cmd/karakuri/serve.go`. `pkg/api` and middleware gate operations; services
own feature behavior. The Svelte frontend builds into `pkg/http/dist`, which
the server binary embeds and serves. Index and other unhashed files revalidate by a
content-hash ETag, hashed `assets/` files are cached as immutable, and a missing
hashed asset is a 404 rather than the app shell. A binary built without the bundle
answers the web interface with a 503 naming the build command. Plugins and themes
stay resource directories. The SQLite driver runs WebAssembly in-process;
server and agent builds do not require cgo. PostgreSQL and MySQL use GORM drivers.

Keep installation path resolution in `internal/resources` and configuration in
`internal/config`. The Makefile creates the development layout and user unit;
`install.sh` installs the files for a user or system installation and creates a
system service's account; it never registers a service, which
`karakuri service install` does through `internal/service`. Resource lookup uses an adjacent
directory alone when present; otherwise it merges user/system directories with the
first copy of a name winning. Config lookup prefers binary-local, user, then system paths.

## State and data model

`pkg/models` owns database records; `pkg/migrate` performs schema and seed
updates. File-backed SQLite enables WAL, a five-second busy timeout, and
immediate write transactions when callers have not supplied pragmas. API keys,
session records, connection ciphertext, and agent CA state have distinct owners.
Database replacement and encryption-key regeneration are not upgrade steps.

## Contracts and failure handling

REST routes under `/api` use bearer authentication and registered permissions.
Access JWTs and refresh JWTs have distinct audiences. `pkg/auth` owns password
verification and token issuance; `pkg/permissions` resolves grants. `pkg/crypto`
uses AES-256-GCM for encrypted settings. Unknown WebSocket topics are denied by
`pkg/mq`; public dashboard exceptions use the scope contract in spec 007.
Health and metrics behavior belongs to their handlers rather than UI routes.

Both JWT kinds require the matching live session row. A refresh JWT carries a `gen`
claim; refresh advances `sessions.refresh_generation` with a conditional update from
that value, so only one exchange per generation succeeds. A loser whose generation is
one behind within `auth.RefreshReuseGrace` (30 seconds) of `refresh_rotated_at` gets
the current generation's pair; any other mismatch deletes the session and logs
`replayed refresh token`. The columns arrive through AutoMigrate at 0, and existing
refresh JWTs without `gen` read as generation 0, so live sessions keep working. Login failures are throttled by username/address in
process memory. REST bearer auth accepts API keys; WebSocket identity accepts access
JWTs or render-job tokens and also admits gated anonymous subscriptions.

Socket clients keep the credential they connected with and the dashboard each
subscription named. `Hub.watch` revalidates every socket every 30 seconds and whenever
`Hub.Recheck` is called, batching requests that arrive within 250 ms. The router's
`recheckSockets` middleware calls it after each successful API or MCP write, plugin gate
removal calls it, and render settlement calls it. Revalidation re-runs
`auth.ValidateToken` or the render resolver, then each subscription's gate check;
`TestRevalidateWithdrawsRevokedSubscriptions` pins the withdrawal. Forwarding headers
are read by `trustedRealIP` only from peers in `server.trusted_proxies`, and
`TestTrustedRealIP` pins the right-to-left reading.

API key limits (FR-017 to FR-020) live on the `api_keys` row as `limited` and a JSON
`permissions` list, added by AutoMigrate; existing rows read as unlimited. The auth
middleware puts a limited key's list on the request context with
`auth.WithKeyLimit`, and `auth.Can(ctx, userID, permission)` is the one permission
check made for a request: the user's roles must grant it and, when the context
carries a limit, a listed string must match it. `RequirePermission`, the MCP tool
gate, handler checks, and the access functions of `flowops`, `dashops` (including
chart query checks), and `uploads` take the request context for this, so their
manage overrides follow the key. Engine work, socket gates, and plugin RPC callbacks
run with no request context and no key limit; sockets never authenticate with API
keys. The plugin proxy forwards the limit as `key_limited` and `key_permissions` on
`HTTPRequest`, and `httpsdk.Allowed` and `RequirePermission` require a listed match
beside the host's user check. Creation runs `auth.Covers` on each string against the
creator's roles and the creating key's limit, refuses an unlimited key from a
limited one, and validates segment syntax and a 64-string bound.
`TestLimitedKeyHoldsOnlyItsPermissions`, `TestKeyLimitCannotExceedItsCreator`, and
`TestKeyLimitFollowsRoleChanges` pin these. The role editor and the key form share
`PermissionList.svelte`; the key form completes against a tree built from
`GET /api/api_keys/permissions` by `treeFromPermissions`.

`config rotate-key` stages all stored ciphertext, including CA and agent credential
keys, then commits a database transaction. It stages a config file before this commit
but renames it afterward. A failed final rename requires installing the reported key;
the database/config pair has no single atomic commit. Long-running MCP, chat, and
synchronous webhook handlers clear the ordinary HTTP write deadline.

## Constitution check

- Keep the authorization and failure boundaries described above explicit.
- Route authoring through shared operations and preserve draft/deployment separation
  where applicable.
- This baseline adds no schema, wire, identity, or encryption-format migration. Future
  changes must assess the affected persistent state before implementation.
- Use the component checks below and the service handoff checks in `AGENTS.md`.

## Verification

Run `make dev TARGET=server` and `make test TARGET=server`. Authentication, throttling,
API-key storage, database, and migration tests cover identity and persistence
boundaries. For operational checks restart the user unit, verify status, and request the
HTTP listener. Installation changes also require a disposable install/upgrade layout.

These are the acceptance checks for changes to this feature. They do not claim
that every live scenario ran while the baseline documents were written.

## Risks and limits

Authentication administration is exposed through REST and the CLI, not MCP. This spec
does not promise high availability or database replication.

New behavior needs reviewed requirements and an update to this plan.

## References

- [Server and agent audit](../server-agent-audit.md)

- [System architecture](../architecture.md)
- [README.md](../../README.md)
- [internal/config/server.go](../../internal/config/server.go)
- [pkg/auth/service.go](../../pkg/auth/service.go)
- [pkg/database/database.go](../../pkg/database/database.go)
- [pkg/models/api_key.go](../../pkg/models/api_key.go)
- [pkg/migrate/migrate.go](../../pkg/migrate/migrate.go)
- [pkg/api/router.go](../../pkg/api/router.go)
