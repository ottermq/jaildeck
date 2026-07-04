# Jail Deck — UI Specification

## Status

Draft. This document defines the initial UI direction, page structure, component strategy, and interaction model.

## UI goal

Jail Deck should feel like a practical FreeBSD administration panel: clear, fast, restrained, and focused on operational confidence.

The interface should help the user answer:

- What jails exist?
- What is running?
- What changed?
- What can I safely do next?
- What failed, and why?

## UI personality

The UI should be:

- calm
- utilitarian
- readable
- predictable
- fast
- explicit about risk
- helpful to someone learning FreeBSD
- efficient enough for repeated daily use

It should avoid feeling like a marketing dashboard or a generic cloud console.

## Visual direction

Initial styling should use plain CSS.

Priorities:

- readable tables
- clear status badges
- obvious action buttons
- compact detail panels
- good spacing
- useful empty states
- visible errors
- responsive enough for laptop and desktop screens

Advanced visual polish can come later.

## Layout

The initial layout should use a classic admin structure:

```text
+--------------------------------------------------+
| Top bar: Jail Deck / host summary / user actions |
+----------------------+---------------------------+
| Sidebar              | Main content              |
|                      |                           |
| Dashboard            | Page title                |
| Jails                | Page actions              |
| Storage              | Content                   |
| Logs                 |                           |
| Settings             |                           |
+----------------------+---------------------------+
```

The sidebar should stay simple. Avoid too many sections in the first version.

## Navigation

Initial navigation:

- Dashboard
- Jails
- Storage
- Logs
- Settings

Possible later navigation:

- Snapshots
- Tasks
- Services
- Packages
- System

These can remain hidden until the features justify their own pages.

## Page specifications

## Dashboard

Purpose: provide a quick operational overview.

Possible content:

- host name
- FreeBSD version
- number of running jails
- number of stopped jails, if known
- storage summary when ZFS is available
- recent operations
- recent errors
- quick link to jails

MVP version can be minimal. The Jails page is more important initially.

## Jails page

Purpose: show all known jails and allow common operations.

Primary content:

- jail name
- status
- JID when running
- hostname
- IP address or addresses
- path
- quick actions

Suggested table columns:

```text
Status | Name | JID | Hostname | IP | Path | Actions
```

Actions:

- View
- Start
- Stop
- Restart

Action availability should reflect current state.

For example:

- running jail: Stop, Restart, View
- stopped jail: Start, View, if configured jails are known
- unknown state: View, Refresh

## Jail detail page

Purpose: inspect one jail more deeply.

Possible sections:

- summary card
- status
- network information
- root path
- services
- storage/dataset information
- recent logs
- recent operations
- actions

The detail page should be useful even before all sections are fully implemented. Unsupported sections should say why they are unavailable.

## Storage page

Purpose: inspect storage relevant to jails.

Initial content:

- ZFS availability
- datasets relevant to jails, if detectable
- used space
- available space
- mountpoints
- snapshots, later

The page should gracefully handle systems without ZFS.

## Logs page

Purpose: central place to inspect recent Jail Deck operations and relevant system logs.

Initial content:

- recent Jail Deck operation results
- command failures
- relevant jail log snippets, where feasible

This page can start simple and become more useful after task history exists.

## Settings page

Purpose: expose safe configuration options.

Early settings may include:

- HTTP bind address display
- detected FreeBSD paths
- detected ZFS availability
- command paths
- application version

Avoid dangerous editable settings in the first version.

## Component strategy

The UI should be built from reusable server-rendered components.

Important initial components:

- `jail_row`
- `jail_status_badge`
- `jail_action_buttons`
- `jail_summary_card`
- `dataset_row`
- `snapshot_row`
- `alert`
- `operation_result`
- `empty_state`
- `confirm_action`
- `section_card`

Components should be useful both in full-page rendering and HTMX responses.

## HTMX interaction patterns

HTMX should enhance server-rendered pages without becoming a hidden frontend framework.

## Pattern: update one jail row

Action:

```text
POST /jails/{name}/start
```

Response:

```text
components/jail_row.html
```

Result:

Only that jail row is replaced.

## Pattern: update action buttons

When an operation changes jail state, the returned fragment should include updated action buttons.

For example, after Start succeeds, the row should show Stop and Restart instead of Start.

## Pattern: show operation result

Mutating actions should provide visible feedback.

Possible target:

```html
<div id="operation-result"></div>
```

A response may update both the row and the operation result area later, but the first version can keep this simple.

## Pattern: refresh section

A Refresh button can reload a specific section without reloading the whole page.

Examples:

- refresh jail list
- refresh logs
- refresh storage summary
- refresh service status

## Pattern: long-running task

For operations that may take longer, use one of:

- HTMX polling
- task status component
- Server-Sent Events later

Do not introduce this until there is a real long-running operation.

## States

Every major section should handle these states:

- loading
- empty
- success
- warning
- error
- unsupported

## Empty states

Empty states should explain what is happening.

Examples:

- No running jails were found.
- ZFS was not detected on this host.
- No recent operation history is available yet.
- This jail is stopped, so service status cannot be inspected.

## Error states

Errors should show:

- what operation failed
- short explanation
- relevant command output when safe
- next suggested action when obvious

Avoid generic messages like “Something went wrong.”

## Confirmation behavior

Potentially destructive or disruptive operations should require confirmation.

Examples:

- stop jail
- restart jail
- rollback snapshot
- delete snapshot
- remove dataset

MVP confirmation can be simple. More refined modals can come later.

## Accessibility and usability

The UI should use:

- real buttons for actions
- real links for navigation
- visible focus states
- readable contrast
- form labels
- meaningful table headers
- status text in addition to colors

Status should never rely on color alone.

## Copywriting guidelines

Use FreeBSD-native terminology, but explain risky or confusing operations in plain language.

Good examples:

- “Start jail”
- “Stop jail”
- “Restart jail”
- “View logs”
- “ZFS is not available on this host.”
- “The command failed. Review the output below.”

Avoid vague wording:

- “Launch container”
- “Destroy environment”
- “Magic sync”
- “Unknown error”

## MVP UI priorities

The first UI should prioritize:

1. Jails list
2. Jail detail page
3. Clear operation feedback
4. Basic storage visibility
5. Basic logs visibility
6. Simple settings/status page

## Open UI questions

1. Should the first design be table-first or card-first?
2. Should action confirmations use browser confirm dialogs at first or custom components?
3. Should logs appear inline on jail detail or only on a dedicated logs page?
4. Should the dashboard be meaningful in MVP, or should `/` redirect to `/jails` initially?
5. How much explanatory text should the UI include for FreeBSD learners?
6. Should dark mode be considered early or postponed?
7. Should the sidebar be collapsible or fixed?
