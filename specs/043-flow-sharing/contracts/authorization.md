# Authorization contract: Per-flow levels

Each entry point keeps its existing global permission, checked first by
route middleware or the MCP tool. The level below is then required on the
named flow. A caller holding no level on the flow receives not-found. A
caller holding a level below the requirement receives forbidden.

Levels: faceplate < view < edit < admin. The owner and holders of
`api:flows:manage` hold admin.

## REST

| Route | Global permission | Level |
|---|---|---|
| `GET /flows` | flows:read | Lists flows held at view or above |
| `POST /flows`, `POST /flows/import` | flows:write | Creates a flow; the caller is owner; a `folder_id` needs edit on that folder |
| `GET /flows/{id}`, `/versions`, `/versions/{v}` | flows:read | view |
| `GET /flows/{id}/export` | flows:read | view, plus view on every embedded subflow |
| `PUT /flows/{id}` | flows:write | edit; `error_flow_id` needs view on the target; `owner_id` needs manage |
| `PUT /flows/{id}/graph` | flows:write | edit; each newly added shared subflow needs view |
| `PUT /flows/{id}/folder` | flows:write | admin, plus edit on the destination folder |
| `POST /flows/{id}/versions/{v}/restore` | flows:write | edit |
| `POST /flows/{id}/promote_subgraph` | flows:write | edit; the new subflow is owned by the caller |
| `POST /flows/{id}/import_subflow` | flows:write | edit, plus view on the source subflow |
| `POST /flows/{id}/test`, `/rerun` | flows:write | edit |
| `POST /flows/{id}/nodes/{node_id}/chat` | flows:write | edit |
| `POST /flows/{id}/deploy` | flows:deploy | edit |
| `POST /flows/{id}/enable`, `/disable` | flows:deploy | edit |
| `DELETE /flows/{id}` | flows:write | admin |
| `GET/POST/DELETE /flows/{id}/shares` | none beyond authentication | admin |
| `GET /flows/{id}/run` | runs:read | view |
| `GET /folders` | flows:read | Lists folders held at view or above |
| `POST /folders` | flows:write | Creates a folder; a parent needs edit |
| `PUT /folders/{id}` | flows:write | edit to rename; admin to move, plus edit on the new parent |
| `DELETE /folders/{id}` | flows:write | admin |
| `GET/POST/DELETE /folders/{id}/shares` | none beyond authentication | admin |
| `GET /runs/active` | runs:read | Filtered to flows held at view or above |
| `POST /runs/{id}/cancel` | runs:cancel | edit on the run's flow |
| `GET /flows/{id}/waits` | runs:read | view |
| `DELETE /flows/{id}/waits/{node_id}` | runs:cancel | edit |
| `GET /widget_triggers` | flows:read | Filtered to flows held at view or above |
| `POST /widget_triggers/{flow_id}/fire` | flows:trigger, dashboard scope, or public trigger | edit when admitted by flows:trigger; dashboard scope and public triggers unchanged |
| `GET /events` | events:read | `flow` subjects filtered to view or above unless manage |
| `POST /dashboards/{id}/deploy` | dashboards:deploy | edit on every flow a flow-trigger binding names |

## MCP

| Tool | Level |
|---|---|
| `list_flows` | Lists flows held at view or above |
| `get_flow`, `list_flow_versions`, `diff_flow`, `list_flow_nodes`, `get_flow_node`, `list_flow_edges`, `validate_flow_graph` (with `flow_id`) | view |
| `export_flow` | view, plus view on embedded subflows |
| `update_flow`, `save_flow_graph`, `patch_flow_graph`, `restore_flow_version`, `test_flow_node`, `run_flow_test` | edit |
| `deploy_flow`, `set_flow_enabled` | edit |
| `delete_flow` | admin |
| `list_flow_runs`, `get_flow_run`, `inspect_flow_state` | view |
| `list_active_runs` | Filtered to view or above |
| `cancel_run`, `cancel_flow_wait` | edit on the run's flow |
| `find_references` | Only flows held at view or above are scanned |
| `get_document_dependencies` | view on the root; referenced flows below view are reported as unreadable, not expanded |
| `list_events` | Same filter as `GET /events` |

## WebSocket topics

| Topic | Admits |
|---|---|
| `flow:<flowID>:*` | runs:read and view on the flow |
| `editor:flow:<flowID>` | flows:read and view on the flow |
| `run:<runID>` | runs:read and view on the run's flow |

## Unchanged

- Triggered runs, error flows, and subflow resolution at run time run as the
  system.
- `/hook/{token}` webhooks are authorized by their token.
- Dashboard scope continues to authorize presses on frozen bindings.
