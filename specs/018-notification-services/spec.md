# Feature specification: Configured notification services

**Feature**: `specs/018-notification-services`
**Created**: 2026-10-06
**Status**: Implemented
**Input**: Send notifications using every service supported by nikoksr/notify. Settings configure each service and its allowed endpoints.

## User scenarios and testing

### User story 1: Configure services and destinations (Priority: P1)

An operator saves credentials and named allowed endpoints under Settings,
then reuses that service from flows without copying credentials into graphs.

**Why this priority**: Settings own both connectivity and allowed destinations.
**Independent test**: Save, read, edit and restart a configured service. Its
credentials and endpoint targets remain protected and usable.

**Acceptance scenarios**:

1. Given a service, when the operator creates named endpoints, then only those
   endpoint IDs are available to notification nodes.
2. Given saved secrets, when settings are read and saved without changing them,
   then the real values stay private and the unchanged values remain usable.
3. Given malformed credential settings or duplicate endpoint IDs, when settings are saved,
   then the request fails without changing the previous configuration.
4. Given an adapter whose provider is retired or whose implementation cannot
   deliver, when it appears in the catalog, then its unavailable reason is
   visible and sending cannot report success.

### User story 2: Send to an approved endpoint (Priority: P1)

An author selects a configured service and endpoint, supplies a subject and
message using incoming values, and routes failures through the normal error port.

**Why this priority**: This is the requested automation action.
**Independent test**: Run a notification node against a local receiver and
verify the exact payload and destination. Attempt a destination outside settings.

**Acceptance scenarios**:

1. Given an enabled service and approved endpoint, when a node executes, then it
   sends once to that endpoint and preserves the input envelope on success.
2. Given a missing, disabled or removed endpoint, when a node executes, then it
   fails before constructing a sender or performing network activity.
3. Given an arbitrary URL or recipient in a message, when it is supplied as an
   endpoint, then the request is refused unless it is an allowed endpoint ID.
4. Given a provider rejection or cancellation, when delivery fails, then the
   error port receives a useful error without credentials, targets or message
   contents, and the action does not retry automatically.

### User story 3: Use the full service catalog (Priority: P2)

Operators configure any deliverable service variant in the selected upstream
release, with settings describing its credentials and receiver format.

**Why this priority**: The user explicitly chose every upstream service.
**Independent test**: Compare the settings catalog with the upstream service
and variant inventory; verify an explicit adapter or unavailable reason for each.

**Acceptance scenarios**:

1. Given the supported service inventory, when settings are opened, then every
   variant appears with its fields and endpoint format or unavailable reason.
2. Given an upstream adapter with default destinations, when one endpoint is
   selected, then the send does not fan out to other destinations.

### Edge cases

- Empty allowlists, missing subjects or bodies, overlong content, malformed JSON
  subscriptions, and invalid receiver identifiers have explicit validation.
- A timeout or disconnect may leave delivery uncertain. It must not cause a retry.
- Settings edits apply to later sends; an already-started send may complete with
  the configuration it acquired. Revoking an endpoint prevents subsequent sends.
- Draft and standalone tests perform real notification actions.

## Requirements

### Functional requirements

- **FR-001**: Settings MUST offer every service variant in the pinned notify release, with unavailable adapters identified rather than silently succeeding.
- **FR-002**: Each saved service MUST own an explicit allowlist of stable endpoint IDs, labels and provider targets. Nodes MUST select one allowed ID per execution.
- **FR-003**: Service credentials and endpoint targets MUST be encrypted at rest, masked on read, preserved by unchanged-value edits and absent from graphs, discovery responses and execution errors.
- **FR-004**: Configuration writes MUST validate the service, required fields, endpoint uniqueness and target format before changing stored state. An empty allowlist MUST be refused.
- **FR-005**: The notification node MUST accept a configured service, approved endpoint ID, subject and body templates, and a bounded timeout; it MUST preserve the input message on success.
- **FR-006**: Endpoint membership and enabled service state MUST be checked before sender construction. Arbitrary URLs or recipients MUST NOT bypass the allowlist.
- **FR-007**: Sending MUST use notify integration without process-global receiver lists. Selecting one endpoint MUST NOT invoke default or broadcast recipients.
- **FR-008**: Delivery failures MUST use normal error routing without automatic retries. Cancellation and timeout limitations of upstream adapters MUST be described accurately.
- **FR-009**: Settings operations and service discovery MUST share REST/MCP rules. Configuration uses connection read/write permissions; direct sends use connection invocation permission. Flow execution follows existing flow authorization.
- **FR-010**: The editor MUST list endpoints from the selected configured service and refresh that list when the service changes. Removed selections MUST remain visibly invalid instead of switching destination.
- **FR-011**: Existing application databases, encrypted secrets and enrolled identities MUST remain readable without regeneration or migration.

### Key entities

- Notification service: named enabled connection with provider credentials.
- Allowed endpoint: stable ID and label with an encrypted provider target.
- Notification request: service ID, endpoint ID, rendered subject/body and deadline.

## Success criteria

- **SC-001**: Every upstream service variant has a settings entry and either a working adapter or a stated upstream unavailability reason.
- **SC-002**: A local receiver gets the expected notification once; invalid endpoint tests produce no requests.
- **SC-003**: Settings round trips preserve secrets without revealing them, including reordered and removed endpoints.
- **SC-004**: Relevant Makefile builds/tests and MCP/browser checks pass; the development service remains active and answers HTTP.

## Assumptions and scope

- Saved notification services reuse encrypted integration connections and existing permissions.
- One node sends to one endpoint. Authors can fan out deliberately in the graph.
- Provider-specific advanced attachments and interactive messages are outside this change.
- Live paid-service delivery requires credentials; local fixtures verify transport and allowlist contracts without contacting third parties.

## References

- [Connection requirements](../004-connections-devices/spec.md)
- [Flow authoring](../../docs/flow-authoring.md)
- [Upstream library](https://github.com/nikoksr/notify)
