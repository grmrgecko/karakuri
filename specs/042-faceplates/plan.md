# Design plan: Faceplates

**Feature**: `specs/042-faceplates` | **Date**: 2026-11-21 | **Spec**: [spec.md](spec.md)

## Summary

A Faceplate node is one named tab of typed controls whose values feed the
flow through pure value outputs. The flow page shows a tab bar: the flow
editor, then one tab per faceplate in its saved order. Apply saves and deploys.
A faceplate-level share (feature 043) reads and applies faceplates without the
graph. Values live in node config, so they follow the draft, version,
restore, and export rules of feature 002.

## Technical context

- **Language and dependencies:** Go, Svelte 5, and the existing flow, engine,
  flowops, and MCP packages. No new dependencies.
- **Node and validation:** `pkg/flow/faceplate.go` holds the config shape,
  `FaceplatePorts`, and validation. `pkg/nodes/core/faceplate.go` holds the
  node.
- **Service:** `pkg/flowops/faceplate.go` lists, applies, and reorders, and
  `pkg/api/faceplate_handler.go` serves REST.
- **MCP:** `pkg/mcpserver/tools_faceplates.go`.
- **Engine:** `pkg/engine/flatten.go` applies per-instance subflow values.
- **Frontend:**
  - `frontend/src/lib/utils/nodePorts.ts` mirrors the ports.
  - `FaceplateControlsField.svelte` is the authoring list.
  - `FaceplatePanel.svelte` renders a tab.
  - `faceplateSandbox.ts` isolates templates and change scripts.
  - `FlowEditor.svelte` hosts the tab bar.
- **Storage:** node config only, plus a `has_faceplates` flag on flows set at
  deploy, so faceplate-level shares can be listed without reading graphs.
- **Limits:** at most 64 controls per faceplate, 256 KiB per template or
  script, and 10 rounds of script-triggered changes.

## Constitution check

- **Failures:** a pulled faceplate returns its stored values and cannot fail
  at run time. A bad value is refused by validation before save or deploy.
  Apply reports strict validation errors exactly as Deploy does.
- **Drafts and deploys:** an edit-level Apply saves the draft and deploys it,
  as the spec asks. A faceplate-level Apply deploys the current version with
  only value changes, then merges those values into the draft. REST and MCP
  share `flowops`.
- **Permissions:** faceplate routes check the feature 043 level. Templates and
  change scripts are written by editors and run in sandboxed iframes without
  same-origin access or network, so a faceplate viewer's session is never
  exposed to them.
- **Existing state:** the change adds config fields and one boolean column,
  with no migration of existing data.
- **Verification:** `make dev`, `make test`, MCP checks, and browser checks of
  tabs, Apply, templates, and scripts.

## Design and contracts

### Config

```json
{
  "name": "Simple",
  "order": 0,
  "controls": [
    {"id": "max", "label": "Max brightness", "type": "number", "default": 80,
     "min": 0, "max": 100, "step": 1, "description": "..."},
    {"id": "gallery", "label": "Gallery", "type": "string",
     "options": [{"value": "classic", "label": "Classic"}]},
    {"id": "sensor", "label": "Light sensor", "type": "entity"},
    {"id": "curve", "label": "Curve", "type": "array", "template": "<html>..."}
  ],
  "values": {"max": 70},
  "script": "faceplate.onChange(...)"
}
```

- **Controls:** each control is a value output with ID `control.id`. Port
  types: `number`→`number`, `boolean`→`bool`, `string`→`string`, and
  `entity`, `array`, and `object`→`object`.
- **Entity values:** an `entity` value is `{connection_id, entity_id}`, and
  its live state is shown beside the field.
- **Pulling:** a pulled faceplate emits `values[id]`, or `default` when the
  value is absent.
- **Validation:**
  - Lenient validation allows incomplete definitions.
  - Strict validation requires unique slug IDs, labels, and known types, plus
    values within type, range, and options.
  - Both check that every value present matches its type.

### Keys and tabs

- **Keys:** a faceplate is addressed by a key. A root node is `nodeID`. A node
  in a local subgraph is `subgraphID/nodeID`. A faceplate inside a shared
  subflow is `subflowNodeID>innerKey`; its values are stored on the parent's
  subflow node as `faceplate_values[innerKey][controlID]` and applied in
  `flatten` for that instance only.
- **Tab order:** tabs sort by `order`, then name. Reordering rewrites `order`
  in the draft.

### REST

All routes are under `/api/flows/{id}/faceplates`:

| Method and route | Level | What it does |
|---|---|---|
| `GET` | faceplate | Returns tabs: key, name, order, controls, values, script, and source. Editors read the draft; faceplate-level users read the deployed version. |
| `POST /apply` | faceplate | Takes `{values: {key: {control: value}}, comment?}`. Edit and above save the draft with the values and deploy it. Faceplate level deploys the current version with the values, then merges them into the draft. |
| `POST /order` | edit | Takes `{keys: [...]}` and rewrites `order` in the draft. |
| `GET /readings` | faceplate | Returns the current state of every entity a control holds or binds to. The faceplate is the grant, as a dashboard binding is. |

### Listing

`ListFor` adds flows held at faceplate level whose `has_faceplates` is set.
Flow responses carry `has_faceplates`.

### Frontend

- **Opening a flow:** `FlowEditor` fetches `/faceplates` first. When there are
  tabs, it shows the tab bar: Flow editor for view and above, then the
  faceplate tabs, opening on the first faceplate tab.
- **Panel:** `FaceplatePanel` holds pending values across tabs. It draws
  built-in editors, polls readings every 5 seconds, sends changes through
  each faceplate's change script, and posts Apply.
- **Templates:** a template renders in an `<iframe sandbox="allow-scripts">`
  with a content security policy denying network access. The bridge posts
  `init` and `update` messages in and receives `set` messages out. A failing
  template falls back to the built-in editor.
- **Change scripts:** these run in one hidden sandboxed iframe. Each change
  goes to every faceplate's script with all values. The script returns
  `set(tab, control, value)` calls. Rounds stop at 10, and errors show on the
  tab.

### MCP

`get_flow_faceplates` and `apply_flow_faceplates`, under the same levels.
Building faceplates uses the graph tools.

## State and data model

- Faceplate data lives in node config and is versioned with the flow.
- `flows.has_faceplates` is set at deploy from the deployed graph, including
  shared subflows it calls.
- Subflow overrides live in the parent's subflow node config, under
  `faceplate_values`.

## Verification

| Scenario | Evidence |
|---|---|
| Ports and validation | Go unit tests for `FaceplatePorts` and value validation. |
| Pull values | An engine test: a consumer parameter fed by a faceplate gets its value or default. |
| Apply at each level | API tests: edit-level Apply deploys the draft; faceplate-level Apply deploys only values and leaves graph edits undeployed; a bad value is refused. |
| Subflow overrides | A flatten test: two instances get different values. |
| UI | A browser check of tabs, Apply, a template, a change script, faceplate-only access, and the console. |

## Risks and open decisions

- **Readings poll** every 5 seconds rather than streaming, which keeps the
  read path inside the faceplate grant.
- **Faceplate-level Apply** uses the deployed graph, so it can't change
  controls that exist only in a draft.
- **Entity controls** take a connection ID and entity ID as text. A picker
  needs connection read access, which a faceplate-level share may lack.
- **Listing flows** now needs only an account, since a faceplate-level share
  may hold no global flow permission. Per-flow levels decide what is listed.
