# Research: Server utility nodes

## Time and numeric behavior

Decision: reuse `pkg/sunevent`, message path helpers and declarative port rules.
Rationale: schedules already resolve coordinates; the editor resolves path-sensitive
types. Civil local comparisons classify repeated DST hours without choosing an
arbitrary occurrence of a clock endpoint.
Alternatives considered: UTC duration windows misclassify local clock hours at DST;
script-only transforms leave common settings and output types undiscoverable.

## Connection observation

Decision: retain statuses in the manager and expose an atomic snapshot/queue watch.
Rationale: the broker has no replay, and running-driver resolution cannot describe
disabled or failed-start connections. Callbacks must not wait for flow admission.
Generations reject reports from retired drivers.
Alternatives considered: polling misses transitions; a broker subscription and
separate snapshot race startup. Per-protocol triggers duplicate lifecycle contracts.

Research used `pkg/integrations/integrations.go`, `pkg/connmgr/manager.go`,
`pkg/connops/connops.go`, `pkg/mq/broker.go`, and the existing real HA/MQTT peers.
