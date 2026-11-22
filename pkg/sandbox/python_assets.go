package sandbox

// pythonHelperModule is the `karakuri` module made importable to every Python
// script. It bridges to the host over the two mounts the runtime provides:
// a per-run scratch directory for data and synthetic read-only files whose open
// triggers a host-side HTTP or node call. WASI offers no sockets and no custom
// host imports, so a file open is the only callback channel available.
const pythonHelperModule = `
import json

_IO = "/karakuri"
_RPC = "/rpc/http"
_NODE_RPC = "/rpc/node"

# vars, flow, and global_ are the run, flow, and global variable dicts,
# replaced by the wrapper before the script runs. Assigning into them is how a
# script hands values to later nodes, later runs, and other flows. The module
# attribute is global_ because global is a Python keyword; the script's own
# namespace still calls it global.
vars = {}
flow = {}
global_ = {}

# _msg holds the incoming message and _msg_writes collects message-scope
# assignments, which the host applies to the outgoing message.
_msg = {}
_msg_writes = []


def _scope_dict(scope):
    """_scope_dict returns the dict backing a scope, or None for the message."""
    if scope == "run":
        return vars
    if scope == "flow":
        return flow
    if scope == "global":
        return global_
    return None


def _lookup_msg(path):
    """_lookup_msg resolves a dot path against the message. A path starting
    with topic or meta addresses that field; anything else reads the payload."""
    parts = path.split(".")
    if parts[0] in ("topic", "meta", "payload"):
        cur, parts = _msg.get(parts[0]), parts[1:]
    else:
        cur = _msg.get("payload")
    for part in parts:
        if not isinstance(cur, dict) or part not in cur:
            return None
        cur = cur[part]
    return cur


# The variable accessors are named get_var and set_var at module level because
# karakuri.get and karakuri.post are already the HTTP helpers. The wrapper
# binds them into the script's namespace as the bare get and set, which is what
# every language calls them.
def get_var(name="payload", scope="msg"):
    """get_var reads a variable. The default is the message payload."""
    name = name or "payload"
    scope = scope or "msg"
    if scope == "msg":
        return _lookup_msg(name)
    target = _scope_dict(scope)
    if target is None:
        raise ValueError("unknown scope %r: use msg, flow, global, or run" % scope)
    return target.get(name)


def set_var(name, value, scope="msg"):
    """set_var writes a variable. The default is the message payload. A value
    of None clears a variable in the flow, global, and run scopes."""
    name = name or "payload"
    scope = scope or "msg"
    if scope == "msg":
        _msg_writes.append({"path": name, "value": value})
        return
    target = _scope_dict(scope)
    if target is None:
        raise ValueError("unknown scope %r: use msg, flow, global, or run" % scope)
    if value is None:
        target.pop(name, None)
    else:
        target[name] = value


def _load_input():
    with open(_IO + "/input.json") as f:
        return json.load(f)


def _write_output(doc):
    with open(_IO + "/output.json", "w") as f:
        json.dump(doc, f, default=str)


class NodeCallError(Exception):
    """NodeCallError reports a rejected or unsuccessful mediated node call."""

    def __init__(self, doc):
        self.message = doc.get("message") or "node call failed"
        self.node_type = doc.get("node_type", "")
        self.invocation_id = doc.get("invocation_id", "")
        self.sequence = doc.get("sequence", 0)
        self.attempts = doc.get("attempts", 0)
        self.retryable = bool(doc.get("retryable", False))
        self.retry_after_ms = doc.get("retry_after_ms")
        self.details = doc.get("details")
        self.fatal = bool(doc.get("fatal", False))
        super().__init__(self.message)

    def __str__(self):
        return self.message


def _node_argument_error(node_type, message, options=None):
    """_node_argument_error builds a zero-attempt local call failure."""
    invocation_id = ""
    if isinstance(options, dict) and isinstance(options.get("id"), str):
        invocation_id = options["id"]
    return NodeCallError({
        "message": message,
        "node_type": node_type,
        "invocation_id": invocation_id,
        "attempts": 0,
    })


class _Nodes:
    """_Nodes exposes exact item targets, alias attributes, and dynamic calls."""

    def __init__(self, types, aliases):
        self._types = types
        self._aliases = aliases

    def __getitem__(self, name):
        try:
            node_type = self._types[name]
        except (KeyError, TypeError):
            raise KeyError(name) from None
        return self._named(node_type)

    def __getattr__(self, name):
        try:
            node_type = self._aliases[name]
        except KeyError:
            raise AttributeError(name) from None
        return self._named(node_type)

    def _named(self, node_type):
        def invoke(*args):
            return self._invoke(node_type, args, False)
        return invoke

    def call(self, *args):
        return self._invoke("", args, True)

    def _invoke(self, node_type, args, generic):
        config_index = 0
        min_args, max_args = 1, 3
        if generic:
            config_index = 1
            min_args, max_args = 2, 4
            if args and isinstance(args[0], str):
                node_type = args[0]
        if len(args) < min_args or len(args) > max_args:
            raise _node_argument_error(
                node_type,
                "node call expects %d to %d arguments, got %d (config is required)" % (
                    min_args, max_args, len(args)))

        if generic:
            node_type = args[0]
            if not isinstance(node_type, str):
                raise _node_argument_error("", "nodes.call node type must be a string")

        config = args[config_index]
        if not isinstance(config, dict):
            raise _node_argument_error(node_type, "node call config must be a dict")

        inputs = None
        if len(args) > config_index + 1:
            inputs = args[config_index + 1]
        if inputs is not None and not isinstance(inputs, dict):
            raise _node_argument_error(node_type, "node call inputs must be a dict or None")

        options = None
        if len(args) > config_index + 2:
            options = args[config_index + 2]
        if options is not None and not isinstance(options, dict):
            raise _node_argument_error(node_type, "node call options must be a dict or None")
        if options is not None:
            unknown = sorted(set(options) - {"id", "retry", "params"}, key=repr)
            if unknown:
                raise _node_argument_error(
                    node_type, "unknown option %r" % unknown[0], options)
            retry = options.get("retry")
            if retry is not None:
                if not isinstance(retry, dict):
                    raise _node_argument_error(
                        node_type, "node call retry options must be a dict or None", options)
                unknown = sorted(set(retry) - {
                    "max_attempts", "delay_ms", "backoff", "max_delay_ms", "jitter"}, key=repr)
                if unknown:
                    raise _node_argument_error(
                        node_type, "retry: unknown option %r" % unknown[0], options)

        request = {
            "type": node_type,
            "config": config,
            "inputs": inputs or {},
            "options": options or {},
        }
        try:
            with open(_IO + "/node_req.json", "w") as f:
                json.dump(request, f, allow_nan=False)
        except (TypeError, ValueError) as error:
            raise _node_argument_error(
                node_type, "node call must contain only JSON data: %s" % error, options) from None

        # Opening the RPC file makes the host validate and perform the call
        # before the read returns.
        with open(_NODE_RPC) as f:
            f.read()
        with open(_IO + "/node_res.json") as f:
            doc = json.load(f)
        if doc.get("error"):
            raise NodeCallError(doc["error"])
        return doc.get("outputs") or {}


def _load_nodes():
    """_load_nodes returns the capability only when the host staged a catalog."""
    try:
        with open(_IO + "/node_functions.json") as f:
            definitions = json.load(f)
    except FileNotFoundError:
        return None
    return _Nodes(definitions.get("types") or {}, definitions.get("aliases") or {})


class HTTPError(Exception):
    """HTTPError reports a request the host refused or could not complete."""


class Response:
    """Response is one completed HTTP call."""

    def __init__(self, doc):
        self.status = doc.get("status", 0)
        self.ok = bool(doc.get("ok"))
        self.headers = doc.get("headers") or {}
        self.body = doc.get("body", "")

    def json(self):
        """json decodes the response body."""
        return json.loads(self.body)

    def __repr__(self):
        return "<Response status=%d ok=%s len=%d>" % (
            self.status, self.ok, len(self.body))


def http(method, url, headers=None, body="", json_body=None):
    """http performs an outbound request under the host's network policy.

    Returns a Response. Raises HTTPError when the host rejected or could not
    complete the request.
    """
    if json_body is not None:
        body = json.dumps(json_body)
        headers = dict(headers or {})
        headers.setdefault("Content-Type", "application/json")

    request = {
        "method": method,
        "url": url,
        "headers": {str(k): str(v) for k, v in (headers or {}).items()},
        "body": body or "",
    }
    with open(_IO + "/http_req.json", "w") as f:
        json.dump(request, f)

    # Opening the RPC file makes the host perform the call and write the
    # response before the read returns.
    with open(_RPC) as f:
        f.read()

    with open(_IO + "/http_res.json") as f:
        doc = json.load(f)
    if doc.get("error"):
        raise HTTPError(doc["error"])
    return Response(doc)


def get(url, headers=None):
    return http("GET", url, headers=headers)


def post(url, body=None, headers=None):
    return http("POST", url, headers=headers, json_body=body)
`

// pythonMainModule runs the user's script and reports the outcome through the
// scratch directory. It executes the script rather than importing it so a
// syntax error is reported with the user's own line numbers, and it accepts
// either a `handle(msg)` function or a `result` variable as the return value.
const pythonMainModule = `
import json
import sys
import traceback

sys.path.insert(0, "/karakuri")
import karakuri

_ctx = karakuri._load_input()

# vars is the run's variable dict. It shadows the builtin vars() inside the
# script, which is the price of naming it the same in every language; the
# builtin remains reachable as builtins.vars. flow and global hold the two
# persisted scopes; the script namespace can use the name global because only
# the module attribute has to avoid the keyword.
_vars = _ctx.get("vars") or {}
_flow = _ctx.get("flow") or {}
_global = _ctx.get("global") or {}
karakuri.vars = _vars
karakuri.flow = _flow
karakuri.global_ = _global
karakuri._msg = _ctx

# The node catalog file exists only when this run received the engine's node
# invoker. In every other context both the module attribute and global stay
# absent rather than appearing with a disabled value.
_nodes = karakuri._load_nodes()
if _nodes is not None:
    karakuri.nodes = _nodes

# input holds one entry per connected input socket, keyed by port ID. It
# shadows the builtin input() inside the script, the same trade vars, get, and
# set already make; builtins.input stays reachable, and the module attribute is
# karakuri.inputs.
_input = _ctx.get("input") or {}
karakuri.inputs = _input

_globals = {
    "__name__": "__main__",
    "msg": _ctx,
    "payload": _ctx.get("payload"),
    "topic": _ctx.get("topic"),
    "meta": _ctx.get("meta"),
    "input": _input,
    "vars": _vars,
    "flow": _flow,
    "global": _global,
    # get and set shadow the builtins inside the script, the same trade the
    # vars dict already makes; builtins.set stays reachable.
    "get": karakuri.get_var,
    "set": karakuri.set_var,
    "http": karakuri.http,
    "karakuri": karakuri,
    "result": None,
}
if _nodes is not None:
    _globals["nodes"] = _nodes


def _collect(name):
    """_collect returns one scope dict from the script's namespace, or {} if
    the script replaced it with something that is not a mapping."""
    current = _globals.get(name)
    return current if isinstance(current, dict) else {}


try:
    with open("/karakuri/script.py") as _f:
        _source = _f.read()
    exec(compile(_source, "script.py", "exec"), _globals)

    _handler = _globals.get("handle")
    if callable(_handler):
        _value = _handler(_ctx)
    else:
        _value = _globals.get("result")

    karakuri._write_output({
        "ok": True,
        "value": _value,
        "vars": _collect("vars"),
        "flow": _collect("flow"),
        "global": _collect("global"),
        "msg_writes": karakuri._msg_writes,
    })
except BaseException as _error:
    # Helper frames hide the call site and expose the RPC plumbing, so retain
    # only the user's frames in the flow author's error message.
    _formatted = traceback.TracebackException.from_exception(_error)
    _formatted.stack = traceback.StackSummary.from_list([
        frame for frame in _formatted.stack
        if frame.filename not in ("/karakuri/main.py", "/karakuri/karakuri.py")
    ])
    karakuri._write_output({"ok": False, "error": "".join(_formatted.format()).rstrip()})
`
