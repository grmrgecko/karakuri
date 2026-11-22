# REST and MCP contract: Home and overview

All routes require a signed-in user or a bearer key. Each source inside is
gated by the permissions in [plan.md](../plan.md#constitution-check); a
source the caller may not read is left out of the result without an error.

| Method and path | MCP tool | Returns |
| --- | --- | --- |
| `GET /api/home/attention` | `get_attention` | `AttentionResult` |
| `GET /api/home/upcoming?hours=12` | `list_upcoming` (`hours`) | `{items: UpcomingItem[]}`. `hours` is 1 to 24 and defaults to 12. |
| `GET /api/home/unfinished` | `list_unfinished_work` | `UnfinishedResult` |

Responses use the standard `{data: ...}` envelope. The shapes are in
[data-model.md](../data-model.md).

Existing routes the overview uses unchanged:

- `GET /api/runs/active` and run cancellation
- `DELETE /api/flows/{id}/waits/{node_id}?key=`
- `GET /api/events` with `level`, `from`, and `limit`
- `GET /api/dashboards`
- `PUT /api/auth/preferences`
