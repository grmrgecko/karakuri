# Data model: Script node SDK

## Node function contract

The server derives a function contract from each eligible `NodeMeta` entry.

| Field | Meaning |
| --- | --- |
| `name` | Exact node type used as the canonical JavaScript/Python property or Bash command target |
| `aliases` | Optional unambiguous lower-camel properties or command targets mapped to the same type |
| `node_type` | Stable registry identifier used for execution |
| `meta` | The catalog's config fields, ports, descriptions, defaults, choices, help, retry, purity, invocation state, and schemas |
| `identity_required` | Whether invocation needs an explicit stable ID for node stores |

A catalog revision is a digest of the ordered contracts. The editor uses it to
replace stale declarations when plugins or agents change the registry. Function
name collisions do not redirect calls: the exact-type property always works,
and ambiguous aliases are omitted rather than reassigned.

Eligibility excludes triggers, annotations, link and subflow routing constructs,
subflow boundaries, nested scripts, and fan-in nodes that need a stream of graph
messages. All other registered types follow the same contract, including plugin
and agent nodes.

## Node call

| Field | Required | Meaning |
| --- | --- | --- |
| `node_type` | Yes | Registry type to execute; a named function supplies it from its contract |
| `config` | Yes, possibly empty | Ordinary node configuration with stored field IDs |
| `inputs` | When required by the node | Message envelopes keyed by message input port |
| `options.id` | For stateful or unknown-state nodes | Stable author-chosen invocation identity |
| `options.retry` | No | Per-call retry override; the node default applies otherwise |
| `options.params` | For required port fields | Values for fields supplied by value edges in a graph |

One script execution admits at most 100 calls and returns at most 1000 messages
and 1 MiB of JSON result data per call. Calls are sequential. A call that fails
validation performs no node execution. Count exhaustion is fatal even when caught.
Binary payloads use the script sandbox's existing base64 representation.

## Node invocation

A message-path invocation gets a sequence number within its parent script
execution. Its history node ID is unique within that run and its event payload
also carries the parent script ID, node type, invocation ID, and sequence.

The store identity is separate from the history identity. It consists of the
flow ID, parent script node ID, and `options.id`. Existing graph nodes keep their
current store keys. No source position or invocation sequence contributes to a
durable store key.

| Field | Meaning |
| --- | --- |
| `parent_node_id` | Script node that initiated the call |
| `invocation_id` | Author's stable ID, or a derived label for a stateless call |
| `sequence` | One-based admission order within this script execution |
| `node_type` | Executed registry type |
| `attempts` | Attempts used, including the first |
| `outputs` | Every ordinary output port mapped to zero or more messages |
| `error` | Catchable failure, with classified retry details when available |

A failure returned to the script is contained at that boundary. If the script
does not catch it, the script node fails and follows its own error route. Once
any call has begun execution, an outer script retry is suppressed so earlier
calls cannot be replayed.

Pure value execution uses the same call contract but rejects impure types and
uses pure evaluation without an action retry loop.

## Script diagnostic

| Field | Meaning |
| --- | --- |
| `severity` | `error` or `warning` |
| `code` | Stable diagnostic identifier |
| `message` | Author-facing explanation and expected contract where useful |
| `from`, `to` | Source offsets when a precise range is known |
| `line`, `column` | One-based source location for server and graph diagnostics |
| `node_type` | Related registry type for node-call diagnostics |

Draft validation does not reject script diagnostics. Strict flow validation
returns them through the existing detail map. Runtime call validation uses the
same node and invocation checks but does not need source locations.

## Persistence

No new database rows or migrations are required. Function contracts and catalog
revisions are derived on demand. Invocation evidence uses existing run events
and their JSON payloads. Stateful and unknown-state calls use existing transient
and durable node stores under their new namespaced identities. Existing flows, deployments,
connection settings, identities, and encrypted values are unaffected.
