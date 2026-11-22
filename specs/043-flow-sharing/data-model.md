# Data model: Flow sharing

## Flow (existing)

`models.Flow.OwnerID` already exists and is set on create, import, and subflow
creation. This feature starts enforcing it. No column changes.

## FlowAccess (new table `flow_accesses`)

One grant of one level on one flow to one user or one user group.

| Field | Type | Rules |
|---|---|---|
| `id` | uint primary key | |
| `flow_id` | uuid, indexed, not null | The flow. Rows go when the flow is deleted, in the same transaction. |
| `user_id` | uuid, nullable | Exactly one of `user_id` and `group_id` is set. |
| `group_id` | uint, nullable | A user group; members hold the level while they belong to it. |
| `permission` | string, not null | `faceplate`, `view`, `edit`, or `admin`. Anything else is refused on create. |
| `created_at` | time | |

It has the same shape as `DashboardAccess`, so the share dialog, the store,
and the handlers follow the same pattern.

## FolderAccess (new table `folder_accesses`)

The same shape as `FlowAccess`, with `folder_id` (uint, indexed) instead of
`flow_id`. A folder share reaches the folder's subfolders and every flow
inside them. `folders.owner_id` already existed and now ranks as admin on the
folder and everything beneath it.

## Levels

Ranks: faceplate 1, view 2, edit 3, admin 4. A request for a level is met by
any grant of that rank or higher.

| Source | Rank held |
|---|---|
| Owner (`flows.owner_id`) | admin |
| Holder of `api:flows:manage` | admin |
| Direct share | its level |
| Share to any group the user belongs to | its level |
| Owner of, or share on, the flow's folder or any folder above it | admin for the owner, otherwise the share's level |
| None of these | 0: the flow is reported as not found |

The highest rank from any source applies. Group membership is read on each
request, so leaving a group takes effect on the next request, as it does for
dashboards.

## Permission split

| Permission | Meaning after this feature |
|---|---|
| `api:flows:read` | May open the flow list and flows they hold a level on. |
| `api:flows:write` | May create flows, and may change flows held at edit or above. |
| `api:flows:deploy` | May deploy flows held at edit or above. |
| `api:flows:trigger` | Unchanged: fire widget triggers. Dashboard bindings keep their own path. |
| `api:flows:manage` (new) | Admin on every flow. The Admin role's seeded `api:*` already covers it. |

A faceplate-level user needs no global flow permission to use faceplates
(feature 042). The faceplate routes check only the per-flow level, so an
administrator can grant faceplate access to an account that has no flow
permissions at all.

## Lifecycle

- **Create, import, or new subflow:** owner is the caller. No share rows.
- **Delete flow:** share rows are removed in the deletion transaction.
- **Delete user:** that user's direct share rows are removed. Flows they own
  keep `owner_id`; the manage permission can reassign ownership through
  update.
- **Delete user group:** that group's share rows are removed.
- **Restore version:** shares are untouched; they are not versioned.
- **Backup and restore:** the table travels with the database archive.

## Migration

`AutoMigrate` creates `flow_accesses`. `api:flows:manage` is registered in
the permission catalog; the Admin role's `api:*` grant already matches it, so
no role seed changes. Existing flows already carry owners. On the development
database every flow belongs to `admin`, which holds manage, so nothing
changes for that account. No data rewrite and no effect on secrets,
identities, or CA material.
