# Node contracts: Agent action options

New configuration follows [data-model.md](../data-model.md). Existing defaults remain.

Command/Script output keeps the decoded stdout payload by default. Property destination
stores stdout at result_property and retains other content. File/discard stdout passes
input payload through and omits decoding. Metadata records exit_code and output paths.
Output type follows format only for returned payload; metadata destinations and omitted
stdout preserve input types, while payload descendant destinations require object payloads.

Network Probe remains pure and emits reachable plus healthy. TCP has equal reachable and
healthy. HTTP response status always establishes reachable; expected status/body determine
healthy. fail_if_unreachable fails for unreachable or asserted unhealthy results. Invalid
configuration, cancellation and read overflow remain errors.

HTTP Request is execution-triggered, with in/out/error ports, no retries, and the agent's
network view. URL/header values interpolate against input. Body is read from body_property
as bytes/text/JSON. Explicit binary body format decodes base64 message payloads;
JSON format serializes the selected value. Output format determines out type; status, response headers and elapsed
milliseconds appear in metadata. Unexpected configured statuses fail. Empty successful
HEAD/204/205 responses return null in auto/JSON formats.

Service Control preserves payload; confirmation adds confirmed and service_status. Failure
uses the existing error branch. Start/restart confirm running, stop confirms stopped,
enable/disable confirm boot policy. No control replay or application health claim.

Write File create-only exclusively installs complete staged data. SHA-256 mismatch/missing
regular file leaves target untouched. Guards are observed checks, not cross-process CAS.

Image Match retains canonical image pixel matches. capture reports mapping_known and
optional desktop_mapping and desktop_bounds; mapped matches add desktop_bounds/desktop_center.
Binary input and unknown providers carry no desktop mapping.

Windows Service Status now reads native SCM state instead of parsing localized
sc.exe output. Its former raw `details` text is omitted; consumers reading that
field must use `state`/`running`. Existing identities and stored data are unaffected.
