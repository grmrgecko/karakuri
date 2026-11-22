# Data model: Agent action options

All fields are per-node configuration or per-execution results. No database migration.

- `accepted_exit_codes`: absent means [0]; explicit list must be nonempty nonnegative integers.
- `stdout_mode`, `stderr_mode`: return (default), file, discard. File requires its path;
  discard forbids its path. Returned bytes have the existing 32 MiB cap.
- `result_destination`: payload (default) or property. `result_property` must name a
  payload/meta descendant; existing parents must be objects. No stream return preserves payload.
- HTTP `expected_statuses`: optional comma-separated 100-599 codes/ranges. Probe absent
  accepts all responses for reachability; Request absent accepts 200-299.
- HTTP method: Probe GET/HEAD, Request GET/HEAD/OPTIONS/POST/PUT/PATCH/DELETE.
- HTTP `headers`: string map; `follow_redirects`: true by default, max ten hops.
- Probe `body_contains`: optional substring; requires GET; complete body must fit output cap.
- Request `body_format`: auto (bytes/text/JSON), json, binary (bytes or decoded base64).
- Request `body_property`: empty means no body; `output_format`: auto/json/text/lines/binary.
- Service `confirm`: false; `confirmation_timeout`: 30s; `confirmation_interval`: 250ms.
- File mode: overwrite/append/delete plus create. `preserve_permissions`: true;
  `expected_sha256`: optional 64 hexadecimal digits, replacement only.
- Capture mapping: optional origin_x/y and units_per_pixel_x/y, finite positive scale;
  desktop rectangle and per-match bounds/center derive from these values.
