# Jail Deck — UI Specification

## Status

Draft. Reset 2026-08-16. The UX goals, page purposes, and copywriting guidance below mostly carry over unchanged — they're independent of frontend technology. What's gone is everything specific to server-rendered HTML + HTMX (JD-009 in `docs/DECISIONS.md` replaced that with a Vue SPA over a JSON API), and that section is now a set of open questions instead of settled patterns, since the Vue architecture itself hasn't been designed yet.

## UI goal

Unchanged. Jail Deck should feel like a practical FreeBSD administration panel: clear, fast, restrained, and focused on operational confidence. The interface should help the user answer:

- What jails exist?
- What is running?
- What changed?
- What can I safely do next?
- What failed, and why?
- (new) What templates are ready to create a jail from?
- (new) What's using my ZFS storage?

## UI personality

Unchanged: calm, utilitarian, readable, predictable, fast, explicit about risk, helpful to someone learning FreeBSD, efficient for repeated daily use. Should not feel like a marketing dashboard or generic cloud console.

## Visual direction

No longer tied to "plain CSS initially" as a technology constraint (Vue opens up more options), but the priorities are unchanged: readable tables, clear status badges, obvious action buttons, compact detail panels, good spacing, useful empty states, visible errors. Specific styling approach (plain CSS, a utility framework, component library) is an open question — see below.

## Layout

The original sidebar + top bar admin layout sketch still holds as a starting point:

```text
+--------------------------------------------------+
| Top bar: Jail Deck / host summary / user actions |
+----------------------+---------------------------+
| Sidebar              | Main content              |
|                      |                           |
| Dashboard            | Page title                |
| Jails                | Page actions              |
| Storage              | Content                   |
| Templates            |                           |
| Logs                 |                           |
| Settings             |                           |
+----------------------+---------------------------+
```

`Templates` is new relative to the original sketch, reflecting the release-template domain. Keep the sidebar simple — avoid too many sections before the features justify their own pages.

## Navigation

Initial: Dashboard, Jails, Storage, Templates, Logs, Settings.

Possible later: Snapshots (if it earns a page separate from Storage), Tasks (once long-running operations exist), Services, Packages, System.

## Page specifications

### Dashboard

Unchanged purpose: quick operational overview. Possible content: host name, FreeBSD version, running/stopped jail counts, storage summary, recent operations/errors, quick links. MVP can stay minimal — Jails and, now, jail creation are the more important flows.

### Jails page

Unchanged core purpose and columns:

```text
Status | Name | JID | Hostname | IP | Path | Actions
```

Actions: View, Start, Stop, Restart, and now **Create** (opens the jail-creation flow). Action availability still reflects current state (running: Stop/Restart/View; stopped: Start/View; unknown: View/Refresh).

### Jail creation flow (new)

Purpose: turn the manual clone-configure-start sequence (`docs/SPEC.md`) into a guided flow.

Likely steps:

1. choose a ready template (only templates with a `@base` snapshot are selectable)
2. name the jail
3. review/adjust generated config (network interface, any service-specific stanza)
4. confirm and create — show per-step progress/result (clone, config write, start) since this is a multi-step operation that can partially fail

Exact interaction shape (single form vs. wizard steps, how failures mid-flow are presented) is not designed yet.

### Jail detail page

Unchanged sections: summary card, status, network info, root path, services, storage/dataset info, recent logs, recent operations, actions. Should now also show which template/release the jail was created from, if known.

### Storage page

Purpose: inspect ZFS storage relevant to jails. Since ZFS is now required (not optional, JD-006), this page doesn't need a "no ZFS" empty state the way the original draft assumed — but should still handle "no datasets under management yet" gracefully. Content: datasets, mountpoints, used/available space, snapshots.

### Templates page (new)

Purpose: manage FreeBSD release templates — the thing jail creation depends on.

Likely content:

- list of prepared templates (release version, patch level, state: fetching/extracting/patching/updating/ready, snapshot name)
- action to prepare a new template for a release
- (open question) a release picker sourced from `download.freebsd.org`, or manual version entry, depending on how the "which releases are supported" question resolves

### Logs page

Unchanged purpose and content: recent Jail Deck operation results, command failures, relevant jail log snippets where feasible.

### Settings page

Unchanged: HTTP bind address, detected FreeBSD paths, detected ZFS pool/dataset root, command paths, application version. Still avoid dangerous editable settings.

## Frontend architecture (open — not designed yet)

Everything below was previously settled for HTMX and is now genuinely open for Vue. Don't treat any of this as decided:

1. **State management** — Pinia, plain composables, or something else?
2. **Routing** — Vue Router, and what the route structure looks like (does it mirror the API path structure from `docs/ARCHITECTURE.md`?)
3. **Component library / styling approach** — plain CSS (continuing the original minimalism), a utility framework, or a component library?
4. **API client pattern** — hand-rolled `fetch` wrappers, a generated client, or something like TanStack Query for caching/refetch?
5. **Real-time/refresh pattern** — replaces HTMX's row-swap-on-mutation pattern. Polling? Optimistic updates? Plain refetch-after-mutation?
6. **Long-running operation UI** — once JD-008 resolves, how does the frontend represent a multi-minute template-preparation operation? (progress bar, polling a status endpoint, toast-on-completion?)
7. **Build tooling** — Vite is the likely default given the Vue ecosystem, but not confirmed.

These should be settled together when Phase 6 (`docs/ROADMAP.md`) actually starts, not decided piecemeal ahead of time.

## States

Unchanged: every major section should handle loading, empty, success, warning, error, unsupported. Now also applies to multi-step flows (jail creation, template preparation) at the step level, not just the page level.

## Empty states

Carried over, plus new examples:

- "No running jails were found."
- "No recent operation history is available yet."
- "This jail is stopped, so service status cannot be inspected."
- (new) "No templates are ready yet — prepare one before creating a jail."
- (new) "No datasets found under the jails storage root."

## Error states

Unchanged: show what operation failed, a short explanation, relevant command output when safe, and a next suggested action when obvious. Avoid generic messages like "Something went wrong." For multi-step flows, show *which step* failed, not just that the overall flow failed.

## Confirmation behavior

Unchanged principle, expanded scope: potentially destructive or disruptive operations require confirmation. Examples now include dataset removal and snapshot rollback (already listed originally) as concrete near-term features rather than hypothetical ones, since storage operations are now actively being built.

## Accessibility and usability

Unchanged: real buttons, real links, visible focus states, readable contrast, form labels, meaningful table headers, status text in addition to color.

## Copywriting guidelines

Unchanged. Use FreeBSD-native terminology, explain risky operations in plain language. Good: "Start jail", "Create jail from template", "ZFS is not available on this host." Avoid vague wording: "Launch container", "Destroy environment", "Unknown error."

## Open UI questions

Carried over from the original, still open:

1. Table-first or card-first for the Jails page?
2. Browser `confirm()` dialogs or custom confirmation components?
3. Should logs appear inline on jail detail or only on a dedicated logs page?
4. Should the dashboard be meaningful from the start, or should `/` redirect to `/jails`?
5. How much explanatory text for FreeBSD learners?
6. Dark mode: early or postponed?
7. Collapsible or fixed sidebar?

New, from the expanded scope:

8. Wizard-style or single-form jail creation?
9. How is template preparation progress shown, given it can take real time?
10. Does the Templates page need its own space, or does it live as a section of Storage?
