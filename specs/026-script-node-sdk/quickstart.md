# Quickstart: Script node SDK

## Build and automated checks

From the repository root:

```sh
make runtimes
make dev TARGET=server
make dev TARGET=frontend
make test TARGET=server
make test TARGET=frontend
systemctl --user restart karakuri-dev.service
systemctl --user is-active karakuri-dev.service
curl -fsS http://localhost:8080/ >/dev/null
```

The server tests cover invocation, validation, retry containment, purity, store
identity, catalog generation, and strict graph checks. The frontend tests cover
declaration generation, language-service diagnostics, completion, hover,
signature help, and source-offset mapping.

## Safe function run

Use the MCP development token from `build/` as described in `docs/mcp.md`.
Create one disposable manual flow with a script node containing:

```js
const encoded = nodes.base64Encode(
  { property: "payload", target: "payload", encoding: "standard", padding: true },
  { in: msg },
);
return encoded.out[0]?.payload;
```

Run the draft with payload `"karakuri"`. The script result should be
`"a2FyYWt1cmk="`. Inspect the exact run with events enabled. It should contain
the parent script event and a separate `base64-encode` invocation event with
sequence 1 and one attempt. Delete the disposable flow after the check.

For a failure check, use a script that catches `nodes.fail` and then calls
`base64Encode`. Give `fail` two attempts through `options.retry`. The run should
succeed through the fallback, record two failed attempts for the contained
call, and encode the payload once. An outer retry on the script must not repeat
that fallback after a later uncaught error.

Repeat the transform and caught-failure fixtures with Python and Bash. Python
uses `nodes.base64Encode(config, {"in": msg})`, reads
`encoded["out"][0]["payload"]`, and catches `karakuri.NodeCallError`. Bash builds
`inputs="{\"in\":$MSG}"`, captures `node base64Encode` output, reads it with
`jsonget out.0.payload`, and checks the command status to inspect a structured
failure. Both must produce the same encoded value, invocation sequence, and
attempt count as JavaScript. Check the 100-call fatal budget in each language,
and confirm that no result or staged variable write commits after exhaustion. Attempt a background
Bash `node` call and confirm it is rejected before admission. Pipeline participation, computed
or escaped command names, wrappers, aliases, `shopt`, `eval`, traps, and duplicate function declarations must
also be rejected before admission. After a
top-level call, argument-less `jsonget` must still read the original payload stream. Python verification must report an actionable operational error if its wasm asset is
unavailable rather than returning an empty diagnostic list.

## Validation and catalog

Call `GET /api/scripts/node_functions` with the development token. Confirm that
`base64Encode` maps to `base64-encode`, trigger and link types are absent, and
config and output schemas match `GET /api/nodetypes`.

Call `POST /api/scripts/verify` with a missing required `ha-get-state.entity_id`
in each language. Confirm a localized `node-config-required` diagnostic and no
Home Assistant request. Include a Python and a Bash syntax error and confirm
original-source ranges. Verify that the same mistakes save in a draft but fail
strict graph validation. Change the config to a computed value that static
analysis cannot inspect and confirm invalid runtime input is rejected before
action.

## Browser editor check

Sign in with the development account and open a disposable flow script node.
Keep the draft unsaved when checking only editor behavior.

1. Type `nodes.` and request completion. Confirm generated functions appear and
   trigger, link, and nested script functions do not.
2. Complete `base64Encode`, enter its config, and check signature help, required
   options, select values, and hover documentation.
3. Introduce an unknown function, a missing required option, an unknown option,
   a wrong option type, and an unknown property on a closed output schema.
   Confirm five localized diagnostics without running the script.
4. Switch JavaScript to Python, Bash, and back. Confirm the worker and catalog
   adapters reconfigure cleanly, no TypeScript worker starts for Python or Bash,
   and no stale result replaces newer text.
5. In Python, check `nodes.` function completion, config and output members,
   hover, signature help, and server diagnostics. In Bash, check `node` function
   completion, JSON argument help, `jsonget` output paths, hover, signatures, and
   diagnostics. Include Unicode before a diagnostic range.
6. Confirm server flow node functions are absent from MQTT state and payload
   editors and from agent Run Script editors, including Python and Bash shebangs.
7. Inspect the browser console after each interaction and take a settled
   screenshot of completion, hover, signature help, and diagnostics.

The quickstart performs no external device action. Any additional test with an
action node requires an explicitly disposable target and must be reported.

## JavaScript baseline verification record: 2026-10-27

`make dev` and `make test` passed for server, frontend, agent, and automation.
The development service was restarted and answered HTTP requests. The live MCP
checks produced these results:

- Base64 encoding returned `a2FyYWt1cmk=` with separate parent and invocation
  history records.
- `fail` recorded two attempts, the script caught the error, and the fallback
  encoded once.
- An outer three-attempt script policy still produced only one completed call
  before a later script failure.
- Flow variables `shared=from-called-node` and `mine=script` both persisted.
- A wrong call saved as a draft, failed strict validation, and was refused a test
  run. Both editor verification flows and the original smoke flow were deleted.

The production browser worker loaded with HTTP 200. Completion, hover,
signature help, required-input diagnostics, and all five requested error cases
were exercised. Python and agent script editors did not show server node
functions. JavaScript switching recreated analysis correctly. No new console
errors appeared; the flow list continued to show two unrelated 404 responses
for older flows with no retained run.

## Multi-language verification record: 2026-10-27

`make runtimes` installed the pinned CPython 3.12 wasm asset with its checksum
verified. `make dev` and `make test` passed for server, frontend, agent, and
automation. The frontend had zero Svelte diagnostics and 33 passing Vitest
tests. Go temporary files used the ignored `build/tmp` directory because `/tmp`
was full of unrelated work; no unrelated temporary files were removed.

Twelve live MCP scenarios passed, six per language. Python and Bash each
returned `a2FyYWt1cmk=`, caught a two-attempt `fail` call and completed the
fallback, suppressed an outer three-attempt script retry after one completed
call, preserved both `shared=from-called-node` and `mine=script`, enforced the
fatal 100-call budget without committing variables, and retained an invalid
draft while strict validation and test execution rejected it. Every disposable
flow was deleted. Live `/api/scripts/verify` checks also covered Python and Bash
required fields, unknown options, required inputs, and UTF-8 ranges after an
astral character.

The production browser checks covered Python and Bash function completion,
config and output members, hover, active-argument signatures, and all five
authoring-error classes. Closed-schema `statee` access produced
`node-output-unknown` in both languages. JavaScript switched back to its worker;
Python and Bash used the catalog adapter without starting that worker. The agent
Run Script editor showed no server node functions and made no verification
requests. The disposable editor flow was deleted. Browser evidence is under
`/tmp/opencode/script-sdk/multilang-browser-evidence`; no generated screenshots,
logs, or Playwright state were committed. No new product console errors appeared.
Final review regressions also rejected background and pipeline calls, computed
and escaped commands, wrappers, aliases, and ambiguous function redefinitions before admission,
preserved top-level stdin, verified `karakuri.nodes`, and replaced old-language
diagnostics instead of leaving them on screen.
