# Data model: Configured notification services

- Connection kind: `notification`, using the existing encrypted Config column.
- Config: `service` catalog ID, `credentials` object of service-defined string
  fields, and `endpoints` array. Service selection is immutable after creation.
- Endpoint: `id` (1–64 ASCII letters/digits/underscore/hyphen), `label` (1–120
  bytes), `target` (1–32768 bytes). IDs are unique within the service.
- Credentials: only catalog fields, with required values and secret declarations.
  Inline JSON credential documents stay encrypted; no credentials-file paths.
- Public config: declared credential secrets and every endpoint target replaced
  by the unchanged-value sentinel. IDs and labels remain visible.
- Runtime: immutable driver configuration, cancellation on stop, and process-wide
  four-slot send capacity. No queued messages persist or resume after restart.
- Node config: connection ID, endpoint ID, subject/body templates, timeout 1–120
  seconds default 30. One execution targets one endpoint.
