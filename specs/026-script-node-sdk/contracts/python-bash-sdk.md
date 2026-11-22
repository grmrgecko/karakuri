# Python and Bash script SDK contracts

These bindings use the catalog, invocation request, output messages, limits, and
capability boundary in [script-sdk.md](script-sdk.md). Only their language shapes
differ.

## Python calls

A capable Python script receives `nodes` in its globals and as
`karakuri.nodes`. Exact types use item access, aliases use attributes, and the
generic form accepts a computed type:

```python
import karakuri

config = {
    "property": "payload",
    "target": "payload",
    "encoding": "standard",
    "padding": True,
}
encoded = nodes.base64Encode(config, {"in": msg})
canonical = nodes["base64-encode"](config, {"in": msg})
same = nodes.call("base64-encode", config, {"in": msg})
result = encoded["out"][0]["payload"]
```

The argument lists are:

```text
nodes.<name>(config, inputs=None, options=None) -> dict[str, list[dict]]
nodes.call(node_type, config, inputs=None, options=None) -> dict[str, list[dict]]
```

Config and option field IDs are unchanged. Inputs and outputs are dictionaries
of complete messages. Binary payloads retain the existing base64-string
representation. A node with no required message inputs can omit `inputs` and
`options`; a caller that supplies options must also supply inputs, using `None`
when no messages are needed.

Failures raise `karakuri.NodeCallError`. Its attributes are `message`,
`node_type`, `invocation_id`, `sequence`, `attempts`, `retryable`,
`retry_after_ms`, `details`, and `fatal`. `str(error)` returns `message`.
Validation failures have zero attempts.

```python
try:
    state = nodes.haGetState({
        "connection": "home-assistant-uuid",
        "entity_id": "light.kitchen",
    })
    result = state["out"][0]["payload"]
except karakuri.NodeCallError as error:
    print(error.node_type, error.attempts, error.message)
    result = {"state": "unknown"}
```

An uncaught exception fails the script. Catching a call-budget failure does not
let the script finish, matching JavaScript.

## Bash calls

A capable Bash script receives a `node` builtin. It is not an external command
and cannot execute a host binary:

```bash
config='{"property":"payload","target":"payload","encoding":"standard","padding":true}'
inputs="{\"in\":$MSG}"
encoded=$(node base64Encode "$config" "$inputs")
canonical=$(node base64-encode "$config" "$inputs")
same=$(node call base64-encode "$config" "$inputs")
jsonget out.0.payload "$encoded"
```

`MSG` is the complete first input message as JSON, alongside the existing
`PAYLOAD`, `TOPIC`, `META`, and `INPUT` variables. It lets a script construct a
port-keyed input envelope without a JSON encoder.

The argument lists are:

```text
node <name> <config-json> [inputs-json [options-json]] -> result JSON on stdout
node call <node-type> <config-json> [inputs-json [options-json]] -> result JSON on stdout
```

The generic form accepts a computed type. Config and option field IDs are
unchanged. An empty options argument is not a substitute for `{}`. To supply
options without message inputs, pass `{}` as inputs.

A successful command writes every ordinary output port to stdout as one JSON
object and exits zero. A failed command writes one structured error object to
stdout and exits nonzero. The error fields are `name`, `message`, `node_type`,
`invocation_id`, `sequence`, `attempts`, `retryable`, optional
`retry_after_ms`, optional `details`, and `fatal`.

```bash
state_config='{"connection":"home-assistant-uuid","entity_id":"light.kitchen"}'
if state=$(node haGetState "$state_config"); then
  jsonget out.0.payload "$state"
else
  attempts=$(jsonget attempts "$state")
  node_type=$(jsonget node_type "$state")
  log "$node_type failed after $attempts attempts"
  printf '{"state":"unknown"}\n'
fi
```

Command substitution captures the error document even though the assignment has
a nonzero status. Checking that status handles the failure. An unchecked status
that becomes the script result fails the node under ordinary shell rules. A
call-budget failure remains fatal even when its status is checked.

No node result is delivered to downstream graph edges. Output, retry, purity,
state identity, history, cancellation, and result limits are the same as for
JavaScript. Calls run as simple foreground commands and output flushes before
the script returns. Background and pipeline participation are rejected before
admission because a script cannot safely outlive an invocation or deadlock its
output. Computed or escaped command names, command wrappers, aliases, `shopt`, `eval`, traps,
and duplicate function declarations are rejected because the original-tree check
cannot prove which execution form they reach. Top-level stdin remains available
to other commands.

## Verification

`POST /api/scripts/verify` accepts `language` values `javascript`, `python`, and
`bash`. Python and Bash diagnostics use the same response fields and stable codes
as JavaScript. Source offsets refer to the original document. Parsing and literal
call inspection never execute the submitted script body or a called node.

The strict flow checker selects the verifier from the script node's configured
language. Unsupported languages retain their existing validation behavior.
Computed types, configurations, inputs, options, shell expansions, and Python
expressions are checked at invocation rather than guessed during static analysis.
