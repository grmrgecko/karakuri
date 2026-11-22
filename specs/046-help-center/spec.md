# Feature specification: Help center

**Feature**: `specs/046-help-center`
**Created**: 2026-11-21
**Status**: Implemented 2026-11-21
**Input**: Add a help option to the menu that shows an index of all help documents
and lets the reader move between them.

## User scenarios and testing

### User story 1: Find the guide for a task (Priority: P1)

A user setting up openHAB opens **Help** from the top menu and sees every guide
grouped by area, each with a one-line summary. They open the openHAB guide, read it,
and follow its link to the dashboard authoring guide without leaving the help pages.

**Why this priority**: The guides exist but only a few are reachable from the app,
and only from the editor panels that link to them.
**Independent test**: Open `/help`, open a guide from the index, and follow a link
from it to another guide.

**Acceptance scenarios**:

1. **Given** a signed-in user, **When** they choose **Help** in the menu, **Then**
   the index lists every guide shipped with the server, grouped, with a summary.
2. **Given** the index, **When** the user opens a guide, **Then** it shows the
   rendered guide, a list of its sections, and the other guides beside it.
3. **Given** a guide that links to another guide's section, **When** the user
   follows the link, **Then** the help page opens that guide at that section.
4. **Given** a guide address with a section, such as `/help/dashboard-authoring#variables`,
   **When** it is opened directly, **Then** the guide opens scrolled to that section.
5. **Given** a guide opened from an editor panel, **When** the reader follows a link
   to another guide, **Then** the help page opens that guide.

### Edge cases

- An unknown guide address shows that the guide does not exist and keeps the index.
- A link to an outside site opens in a new tab.
- A signed-out visitor is sent to sign in, as with other app pages.

## Requirements

### Functional requirements

- **FR-001**: The server MUST keep one catalog of the guides it ships, each with an
  ID, title, group, and summary. The help pages, the guide API, and the MCP guides
  MUST all read it, and every guide file shipped in the binary MUST be in it.
- **FR-002**: `GET /api/guides` MUST list the catalog in order without the guide
  bodies, and `GET /api/guides/{id}` MUST return one rendered guide. Both MUST need
  only a signed-in user.
- **FR-003**: The top menu MUST offer **Help** to signed-in users, opening `/help`.
- **FR-004**: `/help` MUST list every guide grouped as the catalog orders them, with
  each summary. `/help/{id}` MUST show the guide with its sections and the guide list.
- **FR-005**: Links between guides MUST open the target guide in the help pages, a
  section link MUST scroll to that section, and outside links MUST open in a new tab.

### Key entities

- **Guide**: ID, title, group, summary, and markdown body shipped in the binary.

## Success criteria

- **SC-001**: Every guide file in the binary appears in the index, and a test fails
  when a file is added without a catalog entry.
- **SC-002**: Every link between guides opens its target guide and section.

## Assumptions

- Guides describe how to use the software, so any signed-in user may read all of
  them, as the guide endpoint already allows.
