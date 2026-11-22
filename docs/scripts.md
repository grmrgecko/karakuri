# Scripting

The script node runs your code in an isolated interpreter, not in the language you have installed
on the server. There is no package manager, no module loader, and no filesystem. A script sees the
message it was given, the variables it is allowed to read, and mediated capabilities such as HTTP.
A script running in a server flow can also call eligible nodes through its language's node binding.

A script can run any logic, but it cannot run programs on the host or reach connections
and credentials directly. When it calls a node, the node runs with the same authority it has on
the canvas.

Each language below is a complete reference on its own. Read the one you are writing in.

## The sandbox

| | JavaScript | Python | Bash |
|---|---|---|---|
| Runtime | built-in ECMAScript interpreter | CPython 3.12 on WebAssembly (WASI) | built-in bash interpreter |
| Always available | yes | when the CPython module is installed | yes |
| Filesystem | none | only the interpreter's own scratch directory, without symbolic links | none, except `/dev/null` |
| Processes | none | none | no external commands at all |
| Sockets | none | none | none |
| Environment | none | four `PYTHON*` variables the runtime sets | only the message variables |
| Network | `http()` only | `karakuri.get/post/http` only | `http` command only |
| Wall clock | real | **frozen at 2022-01-01** | none |

Python runs on a 26 MB CPython WebAssembly module that releases, the Docker image, and builds
from source include. An install built with `PYTHON_SCRIPTS=0` leaves it out. Without it the
language picker still lists Python, and the node fails with a message saying the module is missing.
To add it to such an install, run `make runtimes` in a checkout and copy `build/runtimes/python.wasm`
into the install's `runtimes` directory, or point `script.python_wasm` at the file.

### Limits

Every execution is bounded. The defaults come from the server's `script` configuration, and the
node's own **Timeout** field overrides the wall-clock time.

| Limit | Default | Setting |
|---|---|---|
| Wall-clock time | 30s | `script.timeout` |
| Captured output | 256 KiB | `script.max_output` |
| Outbound requests | 20 per execution | `script.max_http_requests` |
| Each response body read | 8 MiB | `script.max_http_response` |

Running out of time fails the node. Running out of output budget does not. The console keeps the
lines it captured, appends `... output truncated`, and drops the rest.

### Network policy

`script.network` decides how far the script's own HTTP helper can reach. It does not restrict node functions.

- `full` allows any destination the host can reach. This is the default.
- `bogons` allows public addresses only. Loopback, private ranges, link-local, carrier-grade NAT, and
  the other reserved blocks are refused, which keeps scripts away from the LAN, from the server's
  own services, and from cloud metadata endpoints. The check runs against the resolved IP of every
  connection, so a hostname that points at `127.0.0.1` is rejected however it was spelled, and
  redirects are checked the same way.
- `none` refuses every outbound request.

Only `http://` and `https://` URLs are accepted under any policy. Four redirect
hops can be followed; the fifth is rejected.

## The node

These sections apply to the script node in every language.

### Declaring ports

A script node's ports come from the **Inputs** and **Outputs** lists in its config, not from the
node type, so the canvas knows the shape of your script without running it. A port needs an ID; the
label defaults to the ID and the type defaults to `any`. Declaring none leaves the node with one
untyped input and one untyped output.

The editor enforces declared types. An output declared `object` cannot be wired to an input that
wants a `string`. Use `any` when the payload depends on what arrives.

With more than one output declared, the return value is read as an object keyed by port ID, and a
key you leave out is a branch not taken. Each key's value becomes the payload on that branch.
Returning something that is not an object when several outputs are declared fails the node.

### The error branch

A script node always carries an **Error** output, whatever you declared, unless you claimed the
port ID `err` for yourself. A failed script routes there when something is wired to it, and fails
the run when nothing is. On the message path, the node's retry policy applies first unless a node
call has already begun execution; see [Calling nodes](#calling-nodes).

A script pulled through a value edge is not retried. If it fails, the node that asked for its value
fails before it runs, and that node's own error port is not used. Inside an AI agent's tool
branch, the agent receives the failure as the tool's result instead.

### Value edges

A script's output can be wired to another node's **config field** instead of to its input. The
engine pulls the value when the consuming node runs and remembers it for the rest of the run, so a
script feeding three parameters executes once. If that script makes an HTTP call, it makes one call,
not three.

A pulled script runs when something asks for its value, which may not be when anything arrived on
its input ports. A pulled script can call only pure nodes, the ones that compute a value without
side effects. A call to an action node fails before the action starts.

### Calling nodes

Scripts that the server flow engine runs receive a `nodes` global in JavaScript and Python, and a
`node` builtin in Bash. Every eligible node type has a call target named by its
exact type. Types also have a lower-camel alias when that alias is unambiguous. For example, all
three of these JavaScript forms call the same Base64 Encode node:

```js
const a = nodes["base64-encode"](config, { in: msg });
const b = nodes.base64Encode(config, { in: msg });
const c = nodes.call("base64-encode", config, { in: msg });
```

The logical argument list is the same in every language: call target, config, optional input
messages, then optional invocation options. JavaScript and Python spell it:

```text
nodes.<type>(config, inputs?, options?) -> outputs
nodes.call(type, config, inputs?, options?) -> outputs
```

To pass options without inputs, skip the inputs with `undefined` or `null` in JavaScript and `None`
in Python.

Bash uses `node <type> <config-json> [inputs-json [options-json]]` and
`node call <type> ...`, documented in the Bash section.

`config` is required but can be `{}`. Its keys are the same field IDs used by the node's config
panel. `inputs` contains complete messages keyed by input port, so `{ in: msg }` passes the current
message to a node with an `in` port. `options` can contain:

- `id`: a stable invocation ID, required by stateful nodes and contributed nodes whose state
  behavior has not been audited. Do not derive this ID from a source line or loop index when the
  node's state must survive a restart.
- `retry`: the same `max_attempts`, `delay_ms`, `backoff`, `max_delay_ms`, and `jitter` policy a
  graph node accepts. A non-retryable node does not accept an override.
- `params`: values for config fields that a graph would supply through value edges.

A successful call returns every ordinary output port as an array of messages. An output with no
messages is an empty array. Payload, topic, metadata, and grouping survive. Error ports are not
returned because a failed call throws instead:

```js
const encoded = nodes.base64Encode(
  { property: "payload", target: "payload", encoding: "standard", padding: true },
  { in: msg },
);
return encoded.out[0]?.payload;
```

Calls are synchronous and do not execute downstream graph nodes. The engine resolves connections,
checks configuration and inputs, enforces payload types, applies the call's retry policy, and records
each invocation in run history. Repeated calls have different invocation sequences; attempts within
one call keep the same sequence. A call returns at most 1000 messages and 1 MiB of JSON result data.

A failed call raises a `NodeCallError` in JavaScript and Python. Bash returns a structured error
document with a nonzero status. The error includes the node type, invocation ID, sequence, attempt
count, retry classification, and available details. Validation failures have zero attempts. Catch
the exception or check the status to choose a fallback. Leave a JavaScript or Python exception
uncaught, or propagate the Bash status as the script result, to fail the script node and use its
normal error route:

```js
try {
  return nodes.haGetState({ connection: ha, entity_id: "light.kitchen" }).out[0]?.payload;
} catch (error) {
  console.error("state read failed", error.nodeType, error.attempts, error.message);
  return { state: "unknown" };
}
```

Once any call begins execution, the engine no longer retries the outer script node, so a later
error cannot replay earlier calls. Put retries on the individual call. Nothing rolls back the
external effects of earlier calls.

A script execution admits at most 100 calls. Exhausting that budget fails the script even if the
exception or command status is caught. The script's wall-clock timeout includes time spent inside
calls, including waits. The flow's own run timeout is measured separately and does not count time
a run spends parked in a durable wait.

Triggers, annotations, link and subflow routing, nested scripts, and fan-in nodes such as join,
batch, and aggregate are not callable. Nodes whose inputs or outputs cannot be expressed as JSON
or base64 text are also excluded. Plugin and agent nodes follow the same rules. A call to an
unavailable type fails rather than reaching a different implementation.

The `script.network` policy applies only to the script's own `http()` helper. An HTTP node, server
file node, plugin, or agent action called through `nodes` has the same network, file, and host
authority it has on the canvas, and no more.

Called nodes can read and write run, flow, and global variables while the script is running. When
the script finishes, only values the script itself changed are written back. Changes made by called
nodes are preserved unless the script deliberately changed the same key. Called nodes see the
script's initial variable snapshot, not uncommitted script writes.

### Editor completion and checks

The flow JavaScript, Python, and Bash editors take node functions and documentation from the
installed node types. They offer function and member completion, signature help, hover
documentation, and diagnostics for syntax errors, wrong argument counts, missing or unknown options,
incorrect values, and invalid output usage. Conditional fields stay optional in the editor, and the
server checks them against their conditions.

Detailed output properties come from the node's payload schema. A node with only a broad output
type falls back to `any`, so the editor neither invents properties nor rejects valid dynamic
payloads. The server checks computed configuration, which the editor cannot check statically,
before execution.

You can save a draft that has diagnostics. Test runs and deployment reject statically known errors
in the executable script. Static verification never executes a node or integration, and some
checks can only happen when the script runs.

These node functions and diagnostics appear only in supported server flow script editors. MQTT
device scripts, payload transforms, remote-agent scripts, JSON documents, and Markdown do not
receive them.

### Testing a script

**Test** in the editor opens the message the script receives, as JSON you can edit:

```json
{
  "payload": { "example": 1 },
  "topic": "sensors/office",
  "meta": {}
}
```

Add `input` for a script that reads named ports, and `vars`, `flow`, or `global` for one that reads
a variable scope. Running shows what the script printed alongside what it returned.

It runs the real sandbox with the server's own limits and network policy, so what happens here is
what happens in a run, with two differences: **variable writes are reported, not saved**, and
`nodes` and `node` are unavailable. Use an isolated flow-node test or a flow test run for a script
that calls nodes. Those tests can call every eligible node and can perform real actions.

### Variable scopes

Four scopes, however your language spells the accessors. The name defaults to `payload` and the
scope to `msg`.

| Scope | Lives for | Notes |
|---|---|---|
| `msg` | this message | Reads resolve a dot path; a path starting with `topic` or `meta` addresses that field, anything else reads the payload |
| `run` | the whole run | Shared with nodes outside this message's path. Spelled `vars` as an object |
| `flow` | across runs | Persisted |
| `global` | across runs and flows | Persisted |

The engine applies writes to `flow` and `global` **when the script finishes**, not at the moment of
assignment, so two runs touching the same key settle last-writer-wins rather than
serializing. A script that throws, exits non-zero, or times out discards these writes. Once the
script itself finishes they are kept, even if the node then fails because its result has the wrong
shape for its outputs.

---

## JavaScript

Scripts run in a built-in ECMAScript interpreter. Your code is the body of a function, which is why
a top-level `return` works.

### What is available

Most of modern JavaScript through ES6 and beyond: `let`/`const`, arrow functions, template
literals, classes, destructuring, spread, `Map`/`Set`, generators, `Symbol`, `BigInt`, optional
chaining and `??`, `Object.entries`, `Array.prototype.flat`, `String.prototype.padStart`. The
standard globals `JSON`, `Math`, `Date`, `RegExp`, and `encodeURIComponent` are all there, and
`Date` is the real clock.

Not available: `require` and `import`. There is no module system, so a script is one file with no
dependencies. No `fetch`, `setTimeout`, or `setInterval`. No `process`, `Buffer`, `atob`/`btoa`,
`TextEncoder`, or `structuredClone`.

**Everything is synchronous.** There is no event loop. `Promise` exists and `async` functions can be
defined, but `await` is a syntax error and nothing would ever resolve them. `http()` blocks until
the response arrives and returns it, so a request needs no `await`.

Globals installed for you: `msg`, `payload`, `topic`, `meta`, `input`, `vars`, `flow`, `global`,
`get`, `set`, `http`, `console`. Scripts in a server flow also receive `nodes`.

### The message

```js
payload          // the payload on the first input port
topic            // the topic
meta             // the metadata object
msg              // { payload, topic, meta }
input.threshold  // the payload on a port declared "threshold"
```

Every declared input arrives in `input` under its ID, including the first. `payload` is the same
value reached the short way. A port nothing is wired to is `undefined`, so read it with a fallback:

```js
const limit = Number(input.threshold ?? 30);
```

These are a snapshot. Assigning to `payload` or `msg.payload` changes nothing outside the script.

### Returning a value

`return` a value and it becomes the new payload. **Return nothing and the payload passes through
unchanged**, which is what a script that only logs or only writes variables wants.

```js
return { celsius: reading, hot: reading > 30 };
```

With several outputs declared, return an object keyed by port ID:

```js
// Outputs: "hot", "cold"
return reading > 30 ? { hot: reading } : { cold: reading };
```

The value crosses the boundary as JSON, so objects, arrays, strings, numbers, booleans, and null
survive; functions, classes, and cyclic references do not. A script that both returns a value and
writes the payload with `set()` gets the return value, because the engine applies it last.

### Variables

```js
const count = get("count", "flow") || 0;   // read; scope defaults to "msg"
set("count", count + 1, "flow");           // write
const temp = get("payload.temperature");   // dot path into the message
set("payload.tag", "seen");                // writes the outgoing message
set("count", null, "flow");                // null clears
```

`vars`, `flow`, and `global` are also plain objects, and assigning into them is equivalent to
calling `set`:

```js
flow.count = (flow.count || 0) + 1;
```

### HTTP

```js
const res = http("https://api.example.com/status");
const res2 = http({
  method: "POST",
  url: "https://api.example.com/items",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ name: "thing" }),
});
const res3 = http("https://api.example.com/items", { method: "DELETE" });
```

A response is `{ status, ok, headers, body, error }`. `ok` is true for a 2xx status, `headers` have
lower-cased names, and `body` is text. A refused or failed request **does not throw**. It comes back
with `error` set, so check `error` before trusting the body.

```js
if (res.error) throw new Error("request failed: " + res.error);
const data = res.json();   // throws when the body is not JSON
```

### Logging

`console.log`, `.info`, `.warn`, `.error`, and `.debug` all capture to the node's run history, so a
script's logs show up in the run panel without wiring a debug node. Objects render as JSON.

### Binary payloads

A binary payload arrives as **base64 text**, because JSON cannot carry raw bytes. There is no
`atob` and no `Buffer` here, so JavaScript cannot decode one. Put a `base64` node before the script to hand it a decoded value, or pass the text through and let a node
downstream decode it.

### Failure

Throwing fails the node, as does running past the timeout. Syntax errors are reported against your
own line numbers.

```js
if (!payload.id) throw new Error("payload has no id");
```

### Example

```js
const reading = Number(payload.temperature ?? 0);
const limit = Number(input.threshold ?? 30);

if (reading > limit) {
  const res = http({
    method: "POST",
    url: "https://api.example.com/alert",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ topic, reading }),
  });
  if (res.error) throw new Error("alert failed: " + res.error);
  console.log("alerted", res.status);
}

set("last_reading", reading, "flow");
return { reading, limit, over: reading > limit };
```

---

## Python

Scripts run on CPython 3.12 compiled to WebAssembly, under WASI. The interpreter is real CPython
with the real standard library. WASI provides no sockets and no processes, and the only directories
that exist are the interpreter's own.

### What is available

The standard library: `json`, `re`, `datetime`, `math`, `base64`, `hashlib`, `uuid`, `random`,
`collections`, `itertools`, `functools`, `decimal`, `statistics`, `csv`, `io`, `textwrap`, and the
rest of the pure-Python modules.

Not available: `socket` raises `OSError` and `subprocess` reports that WASI has no processes.
`threading` cannot start a thread. `urllib` imports and then fails at connect time, because it needs
the sockets that are not there. Use `karakuri.get` and `karakuri.post` instead. There is no host
filesystem to open.

**The clock is frozen.** Python has no access to the host clock, so `time.time()` and
`datetime.now()` report 2022-01-01 in every run and advance by microseconds regardless of how long
the script takes. Take the time from the message, from a variable, or from a
node that knows it.

**Four names are taken.** `input`, `vars`, `get`, and `set` are the message and variable accessors
inside your script. The Python builtins they shadow stay reachable as `builtins.input`,
`builtins.vars`, and `builtins.set`. Scripts in a server flow also receive `nodes`.

### The message

```python
payload                     # the payload on the first input port
topic                       # the topic
meta                        # the metadata dict
msg                         # {"payload": ..., "topic": ..., "meta": ...}
input["threshold"]          # the payload on a port declared "threshold"
```

Every declared input arrives in `input` under its ID, including the first. `payload` is the same
value reached the short way. A port nothing is wired to is missing from the dict, so read it with a
fallback:

```python
limit = float(input.get("threshold", 30))
```

These are a snapshot. Rebinding `payload` changes nothing outside the script.

### Calling nodes

A script in a server flow receives `nodes` in its globals and as `karakuri.nodes`. Exact
node types use item access, aliases use attributes, and `nodes.call` accepts a computed type:

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

Inputs and outputs are dictionaries of complete messages. Every ordinary output is a list, including
when it is empty. Config, input, and option field IDs are the same ones the node's config panel
uses. To pass options without message inputs, use `None` for inputs. Binary payloads remain base64 text.

A failure raises `karakuri.NodeCallError`. Its attributes are `message`, `node_type`,
`invocation_id`, `sequence`, `attempts`, `retryable`, `retry_after_ms`, `details`, and `fatal`:

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

An uncaught exception fails the script. Catching a call-budget exception does not let the script
finish. Standalone tests, device scripts, and remote-agent
Python scripts do not receive `nodes`.

### Returning a value

Assign to `result`, or define `handle(msg)` and return from it. **Leave `result` unset and the
payload passes through unchanged**, which is what a script that only logs or only writes variables
wants.

```python
result = {"celsius": reading, "hot": reading > 30}
```

```python
def handle(msg):
    return {"topic": msg["topic"]}
```

With several outputs declared, set `result` to a dict keyed by port ID:

```python
# Outputs: "hot", "cold"
result = {"hot": reading} if reading > 30 else {"cold": reading}
```

The value crosses the boundary as JSON, so dicts, lists, strings, numbers, booleans, and None
survive; anything else is rendered as its string form. A script that both sets `result` and writes
the payload with `set()` gets `result`, because the engine applies it last.

### Variables

```python
count = get("count", "flow") or 0     # read; scope defaults to "msg"
set("count", count + 1, "flow")       # write
temp = get("payload.temperature")     # dot path into the message
set("payload.tag", "seen")            # writes the outgoing message
set("count", None, "flow")            # None clears
```

`vars`, `flow`, and `global` are also plain dicts, and assigning into them is equivalent to calling
`set`:

```python
flow["count"] = flow.get("count", 0) + 1
```

The module attribute for the global scope is `karakuri.global_`, because `global` is a Python
keyword, but your script's own namespace still calls it `global`.

### HTTP

```python
import karakuri

res = karakuri.get("https://api.example.com/status")
res = karakuri.post("https://api.example.com/items", {"name": "thing"})   # sends JSON
res = karakuri.http("PUT", url, headers={"X-Key": "..."}, body="raw text")
```

A `Response` carries `status`, `ok` (a 2xx status), `headers` with lower-cased names, and `body` as
text, plus `json()` to decode it. A refused or failed request raises `karakuri.HTTPError`.

```python
try:
    res = karakuri.get(url)
    data = res.json()
except karakuri.HTTPError as err:
    raise RuntimeError("request failed: %s" % err)
```

### Logging

`print()` captures to the node's run history, so a script's logs show up in the run panel without
wiring a debug node.

### Binary payloads

A binary payload arrives as **base64 text**, because JSON cannot carry raw bytes. Python is the one
language here that can decode it in the script:

```python
import base64
raw = base64.b64decode(payload)
result = base64.b64encode(raw).decode()   # base64 text going back out
```

### Failure

Raising fails the node, as does running past the timeout. The traceback shows only your own frames.

```python
if "id" not in payload:
    raise ValueError("payload has no id")
```

### Example

```python
import karakuri

reading = float(payload.get("temperature", 0))
limit = float(input.get("threshold", 30))

if reading > limit:
    try:
        res = karakuri.post("https://api.example.com/alert", {"topic": topic, "reading": reading})
        print("alerted", res.status)
    except karakuri.HTTPError as err:
        raise RuntimeError("alert failed: %s" % err)

set("last_reading", reading, "flow")
result = {"reading": reading, "limit": limit, "over": reading > limit}
```

---

## Bash

Scripts run in a built-in bash interpreter that never starts a process. The language is bash; the
command set is not.

### What is available

The shell itself, in full: functions, arrays, arithmetic, `[[ ]]`, `case`, loops, parameter
expansion (`${v^^}`, `${v:1:2}`, `${#v}`, `${v/a/b}`), brace expansion, command substitution, pipes,
redirects, heredocs, `trap`, `local`. Builtins such as `echo`, `printf`, `read`, `test`, `export`,
and `exit` work as usual.

Not available: every external command. There is no `cat`, `date`, `sed`, `awk`, `grep`, `jq`,
`curl`, `sleep`, or `base64`. The interpreter denies anything not in the table below, so a script
cannot reach the host by spawning a process. `/dev/null` is the only path that opens, and `$PATH`
and `$HOME` are empty. The script sees no files. A glob matches nothing and stays literal, file tests such as `[ -e path ]` are always false, and `$PWD` is the root.

Commands provided instead:

| Command | Does |
|---|---|
| `log <message>` | Writes to the run log (stderr), leaving stdout clean |
| `http <method> <url> [body] [header:value ...]` | Performs a request; body to stdout, status to stderr |
| `jsonget <path> [json]` | Reads a dot path out of a JSON document, or out of stdin when the document is omitted. Numeric segments index arrays. An integer prints with the digits the document wrote, at any size |
| `getvar [-s <scope>] [name]` | Prints a variable |
| `setvar [-s <scope>] <name> [value]` | Writes a variable; a value that parses as JSON is stored decoded |
| `node <type> <config> [inputs] [options]` | Server flows only: calls an eligible node through the engine and prints its output or error as JSON |

### The message

```bash
$PAYLOAD   # the payload on the first input port, as JSON, also on stdin
$TOPIC     # the topic
$META      # the metadata, as JSON
$INPUT     # every connected port, as JSON keyed by port ID
$MSG       # the complete first input message, as JSON
```

Read into any of them with `jsonget`. With no second argument it reads stdin:

```bash
reading=$(jsonget temperature "$PAYLOAD")
reading=$(jsonget temperature)              # same thing, from stdin
limit=$(jsonget threshold "$INPUT")         # a port declared "threshold"
```

A path that is not there prints nothing, so fill in a fallback:

```bash
[ -z "$limit" ] && limit=30
```

Scalars print bare so they interpolate cleanly; objects and arrays print as JSON.

### Calling nodes

A script in a server flow receives a `node` builtin. It is part of the sandbox, not an external
command. Use the exact type or an unambiguous alias as the first argument, followed by
config, inputs, and options as JSON documents. `node call` accepts a computed type:

```bash
config='{"property":"payload","target":"payload","encoding":"standard","padding":true}'
inputs="{\"in\":$MSG}"
encoded=$(node base64Encode "$config" "$inputs")
canonical=$(node base64-encode "$config" "$inputs")
same=$(node call base64-encode "$config" "$inputs")
jsonget out.0.payload "$encoded"
```

`MSG` contains the complete first input message, which makes the common `{"in": ...}` envelope
possible without a JSON encoder. Inputs and outputs contain complete messages keyed by port. Every
ordinary output is an array, including when it is empty. To pass options without inputs, pass `{}`
as the inputs argument.

A successful call prints its output object and exits zero. A failed call prints a structured error
object and exits nonzero. The error fields are `name`, `message`, `node_type`, `invocation_id`,
`sequence`, `attempts`, `retryable`, optional `retry_after_ms`, optional `details`, and `fatal`.
Command substitution captures that document in both cases:

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

Checking the status handles the failure. An unchecked status that becomes the script result fails
the node under ordinary shell rules. A call-budget failure remains fatal even when its status is
checked. Calls run as simple foreground commands; a `node` call in the background or in a pipeline
is rejected before it runs. Computed or escaped command names, command wrappers, aliases, `shopt`,
`eval`, traps, and duplicate function declarations are also rejected when `node` is available,
because they could hide a node call from the check that runs before the script starts. Standalone
tests, device scripts, and remote-agent Bash scripts do not receive `node`.

### Returning a value

Standard output is the result: parsed as JSON when it parses, kept as trimmed text when it does not.
**Print nothing and the payload passes through unchanged**, which is what a script that only logs or
only writes variables wants.

```bash
printf '{"celsius": %s, "hot": %s}\n' "$reading" "$hot"
```

With several outputs declared, print an object keyed by port ID:

```bash
# Outputs: "hot", "cold"
# The shell test is integer-only, so trim any decimal before comparing.
if [ "${reading%%.*}" -gt 30 ]; then
  printf '{"hot": %s}\n' "$reading"
else
  printf '{"cold": %s}\n' "$reading"
fi
```

Logging goes to stderr for this reason. An `echo` meant as a progress message would corrupt the
payload.

### Variables

```bash
count=$(getvar -s flow count)        # read; scope defaults to msg
[ -z "$count" ] && count=0
setvar -s flow count $((count+1))    # write
temp=$(getvar payload.temperature)   # dot path into the message
setvar payload.tag seen              # writes the outgoing message
setvar -s flow count                 # no value clears
```

`$VARS`, `$FLOW`, and `$GLOBAL` hold the three non-message scopes as JSON documents to read with
`jsonget`. They are a snapshot. Write with `setvar`.

### HTTP

```bash
body=$(http GET https://api.example.com/status)
body=$(http POST https://api.example.com/items '{"name":"thing"}' Content-Type:application/json)
```

The response body goes to stdout and the status to stderr, so `$(http ...)` captures the body
alone. A refused or failed request exits non-zero, so handle it explicitly:

```bash
body=$(http GET "$url") || exit 1
```

### Logging

`log <message>` writes to the run log, so it shows up in the run panel without wiring a debug node.
Anything else you send to stderr lands there too.

### Binary payloads

A binary payload arrives as **base64 text** in `$PAYLOAD`, because JSON cannot carry raw bytes.
There is no `base64` command in the sandbox, so bash cannot decode one. Put a `base64` node
before the script to hand it a decoded value, or pass the text through and let a node downstream
decode it.

### Failure

Exiting non-zero fails the node, as does running past the timeout. Nothing else does. A command that
fails mid-script leaves the script running unless you check it.

```bash
[ -n "$reading" ] || { log "payload has no temperature"; exit 1; }
```

### Example

```bash
reading=$(jsonget temperature "$PAYLOAD")
[ -z "$reading" ] && reading=0
limit=$(jsonget threshold "$INPUT")
[ -z "$limit" ] && limit=30

# The shell test is integer-only, so trim any decimal before comparing.
if [ "${reading%%.*}" -gt "${limit%%.*}" ]; then
  http POST https://api.example.com/alert \
    "{\"topic\":\"$TOPIC\",\"reading\":$reading}" \
    Content-Type:application/json > /dev/null || exit 1
  log "alerted for $reading"
fi

setvar -s flow last_reading "$reading"
printf '{"reading": %s, "limit": %s}\n' "$reading" "$limit"
```
