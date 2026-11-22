# Research: Agent action options

## Process output and files

Decision: Share explicit stream policies between Command and Script; keep existing return
behavior and file mirrors by default. Expose permission preservation, which already exists.
Rationale: File mirrors currently still trigger the 32 MiB return cap. Exclusive creation
must avoid the check-then-rename overwrite race. Use staged hard links and fail unsupported
filesystems rather than fall back to unsafe overwrite.
Alternatives: Returning truncated output obscures malformed JSON and partial data. A
filesystem existence check alone cannot implement create-only safely.

## HTTP and service state

Decision: Use an agent-only bounded HTTP helper and disable HTTP request retries. Retain
reachability and add separate health results. Read numeric SCM states/startup type and
launchd disabled overrides for confirmation.
Rationale: Server HTTP imports engine dependencies. Agent errors cross as strings and
cannot retain method-sensitive retry classification. running=false does not imply stopped.
Alternatives: Sharing the server node or relying on its retry errors expands unrelated
protocol/server behavior. Checking process lists does not establish service enablement.

## Capture geometry

Decision: Publish optional geometry at capture providers and carry it beside helper PNG.
Rationale: Decoded PNG discards image origins; portal captures provide no mapping contract.
Retina logical coordinates cannot be inferred from pixel resolution alone.
Alternatives: Agent-side division of window/display dimensions can misidentify scaling,
clipping, decoration and display index. Unknown mappings must stay unknown.

Research used repository implementations and two read-only research agents requested by
the planning skill. No new third-party API or library is introduced.
