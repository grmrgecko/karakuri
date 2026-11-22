# Design plan: Help center

**Feature**: `specs/046-help-center` | **Date**: 2026-11-21 | **Spec**: [spec.md](spec.md)

## Summary

The `docs` package embeds every guide with `//go:embed *.md` and holds the catalog:
ID, title, group, and summary per guide, in display order. Guides render once and
are cached. The guide API, MCP resources and `read_guide`, the editor assistant, and
the help pages all read the catalog, replacing the separate lists the API handler
and MCP resources kept. The frontend adds `/help` and `/help/:id`, and a shared
`GuideBody.svelte` component renders a guide's HTML with the prose styles and turns
links between guides into help-page navigation.

## Technical context

- **Server:** `docs/docs.go` (`Guides`, `Markdown`, `Get`, `Render`),
  `pkg/api/guide_handler.go` (`GET /guides`, `GET /guides/{id}`),
  `pkg/mcpserver/resources.go`, `pkg/mcpserver/assistant.go`.
- **Frontend:** `routes/Help.svelte`, `lib/components/GuideBody.svelte` (prose styles
  moved out of `GuideDocs.svelte`), `lib/stores/guides.ts` (`index()`), and a Help
  link in `Nav.svelte`.
- **Links:** guides link as `other.md#anchor`. `GuideBody` intercepts clicks on such
  links and calls its `onnavigate(id, anchor)`. The help page navigates to
  `/help/other#anchor`. The editor's modal sends the reader to the help page. A
  bare `#anchor` scrolls in place, and `http(s)` links open in a new tab.

## Constitution check

- **Failures:** an unknown guide ID answers 404, and the page shows that the guide
  does not exist next to the index.
- **Permissions:** guides carry no installation data, so the existing signed-in
  rule stays.
- **Existing state:** no stored data changes. The MCP guide list grows to every
  guide, and existing names keep working.

## Verification

- `make test TARGET=server PKGS=./docs/`: every embedded file has a catalog entry,
  every entry has a file, and links between guides resolve to a guide and anchor.
- Browser: open Help, the index, a guide, a link to another guide's section, and a
  direct `/help/{id}#section` address, then check the console.
