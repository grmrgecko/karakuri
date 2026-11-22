# Data model: Home and overview

## Stored

### User preferences (existing `users.preferences` JSON)

| Key | Type | Meaning |
| --- | --- | --- |
| `start_page` | string, optional | `overview`, a dashboard UUID, or absent for the default. |
| `pinned_dashboards` | array of dashboard UUID strings | Quick launch order. |

The keys are written through `PUT /api/auth/preferences`, which merges.
Neither key is validated against access on the server. The browser resolves
both against the caller's dashboard list, so a stale or foreign ID only falls
back and never reveals anything. No migration is needed.

## Computed per request

### AttentionResult

| Field | Type | Notes |
| --- | --- | --- |
| `items` | AttentionItem[] | Errors first, then newest first. |
| `checked` | string[] | Any of `connections`, `agents`, `flows`, `backups`, `history`. |
| `unchecked` | string[] | Sources the caller may read but that could not be read. |
| `summary` | object | `connections_connected`, `connections_total`, `agents_online`, `agents_total`, `flow_failures` (all optional ints). |

### AttentionItem

| Field | Type | Notes |
| --- | --- | --- |
| `severity` | `error` \| `warning` | |
| `kind` | `connection` \| `agent` \| `flow` \| `backup` \| `history` | |
| `subject_id` | string | Connection, agent, or flow ID, or the backup archive. |
| `subject_name` | string | |
| `message` | string | The error text or a condition such as "unstable: dropped 4 times in the last hour". |
| `count` | int | Repeats; 1 when single. |
| `at` | time | When the condition began, or its newest occurrence. |
| `recovered` | bool | Flow groups whose newest finished run succeeded. |
| `run_id` | string, optional | Newest failed run for flow groups. |
| `link` | string | An app path. |

### UpcomingItem

| Field | Type | Notes |
| --- | --- | --- |
| `kind` | `schedule` \| `wait` | |
| `at` | time | Next firing or deadline. |
| `flow_id`, `flow_name` | | |
| `node_id`, `node_label` | | |
| `key` | string | Waits only; used by the cancel route. |
| `can_cancel` | bool | Waits only. |

### UnfinishedResult

`flows` and `dashboards`, each an array of
`{id, name, updated_at, deployed}`, where `deployed` is false when the item
was never deployed. Either array is absent when the caller lacks the matching
write permission.
