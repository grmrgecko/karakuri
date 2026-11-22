# Script SDK contracts

## JavaScript calls

A capable JavaScript script receives a `nodes` global. Each eligible type has a
canonical property named by its exact type and can also have an unambiguous
lower-camel alias. The generic form remains available for dynamic types.

```js
const encoded = nodes.base64Encode(
  { property: "payload", target: "payload", encoding: "standard", padding: true },
  { in: msg },
);

return encoded.out[0]?.payload;

// Equivalent canonical and generic forms.
const canonical = nodes["base64-encode"](
  { property: "payload", target: "payload", encoding: "standard", padding: true },
  { in: msg },
);
const same = nodes.call(
  "base64-encode",
  { property: "payload", target: "payload", encoding: "standard", padding: true },
  { in: msg },
);
```

Named and generic calls have these argument lists:

```text
nodes.<name>(config, inputs?, options?) -> outputs
nodes.call(nodeType, config, inputs?, options?) -> outputs
```

`config` is required, but can be `{}`. `inputs` contains complete message
objects, not only payloads. `options` has this shape:

```js
{
  id: "stable-state-identity",       // required by stateful and unknown-state types
  retry: {
    max_attempts: 3,
    delay_ms: 500,
    backoff: 2,
    max_delay_ms: 5000,
    jitter: 0.2,
  },
  params: {
    // Config values that a graph would supply through value edges.
  },
}
```

A successful result contains every ordinary output port. Each property is an
array of messages, including when the array is empty. Error output ports do not
appear because failures throw a catchable `NodeCallError` instead.

```js
try {
  const result = nodes.haGetState({
    connection: "home-assistant-uuid",
    entity_id: "light.kitchen",
  });
  return result.out[0]?.payload;
} catch (error) {
  console.error(error.nodeType, error.attempts, error.message);
  throw error;
}
```

`NodeCallError` exposes `name`, `message`, `nodeType`, `invocationId`, `sequence`,
`attempts`, `retryable`, optional `retryAfterMs`, and optional `details`.
Configuration and admission failures have zero attempts.

Calls do not execute downstream graph nodes. They do not make the script
asynchronous and do not add `await` support.

## Function catalog

`GET /api/scripts/node_functions` requires flow read permission and returns:

```json
{
  "schema_version": 1,
  "revision": "sha256-of-ordered-contracts",
  "functions": [
    {
      "name": "base64-encode",
      "aliases": ["base64Encode"],
      "node_type": "base64-encode",
      "identity_required": false,
      "meta": {
        "type": "base64-encode",
        "label": "Base64 Encode",
        "description": "Encodes binary data or text as base64",
        "inputs": [],
        "outputs": [],
        "config_spec": [],
        "pure": true,
        "retryable": false
      }
    }
  ]
}
```

The illustrated empty arrays stand in for the ordinary catalog metadata; the
response does not truncate config fields or port schemas. The editors turn this
document into JavaScript declarations and Python/Bash completion and help for
function, member, signature, hover, and diagnostic information. They do not
maintain a second list of node functions.

The catalog carries only public node metadata. It does not expose connection
credentials or resolved integration configuration.

## Script verification

`POST /api/scripts/verify` requires flow write permission and accepts at most
256 KiB of JavaScript, Python, or Bash source:

```json
{
  "language": "javascript",
  "source": "return nodes.haGetState({ connection: \"home-assistant-uuid\" });",
  "pure": false
}
```

The response is a diagnostic list, not an execution result:

```json
{
  "diagnostics": [
    {
      "severity": "error",
      "code": "node-config-required",
      "message": "required config field \"entity_id\" is missing",
      "from": 24,
      "to": 60,
      "line": 1,
      "column": 25,
      "node_type": "ha-get-state"
    }
  ]
}
```

Positions are best-effort ranges in the original source. A whole-script parser
error can have a zero-length range. Verification parses and validates but never
executes a node or resolves a live integration.

The same verifier backs strict flow test and deployment checks through the
shared flow operation layer. Draft saves remain lenient. Computed expressions
that cannot be inspected are accepted statically and checked again by the
runtime invocation contract.

## Capability boundary

Only an engine execution of the server flow script node receives a node
invoker. Standalone `/scripts/test`, MQTT device scripts, payload transforms,
and remote-agent scripts do not receive `nodes` or `node`. Direct flow-node tests
use the same full callable catalog as a deployed run and retain the existing
warning that tests can perform real actions.
