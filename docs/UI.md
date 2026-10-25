---
title: "Interface"
description: "The web interface: what it shows and the conventions it follows."
---

# Jail Deck — UI Specification

## UI goal

Jail Deck should feel like a practical FreeBSD administration panel: clear, fast, restrained, and focused on operational confidence. The interface should help the user answer:

- What jails exist?
- What is running?
- What changed?
- What can I safely do next?
- What failed, and why?
- What templates are ready to create a jail from?
- What's using my ZFS storage?

## UI personality

Calm, utilitarian, readable, predictable, fast, explicit about risk, helpful to someone learning FreeBSD, efficient for repeated daily use. Should not feel like a marketing dashboard or generic cloud console.

## Visual direction

Priorities: readable tables, clear status badges, obvious action buttons, compact detail panels, good spacing, useful empty states, visible errors. Specific styling approach (plain CSS, a utility framework, component library) is an open question — see "Frontend architecture" below.

## Layout

A classic sidebar + top bar admin layout is the starting point:

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

Keep the sidebar simple — avoid too many sections before the features justify their own pages.

## Navigation

Built: **Dashboard**, **Jails**, **Templates**, **Operations**, **Host**. A section stays highlighted on its detail pages, so `/jails/ottermq` keeps Jails lit.

Still on the sketch above: Storage, Logs, Settings. Possible later: Snapshots (if it earns a page separate from a jail's), Tasks (once long-running operations exist), Services, Packages, System.

## Page specifications

### Dashboard

Purpose: quick operational overview. Built at `/dashboard`, and it is where `/` now lands — "what is this host doing" is the question someone opens the page with, and the jail list is one click away.

Four cards, then three tables:

- **Load**, with the five- and fifteen-minute averages under it and the CPU count, because a load of 4 means something different on 2 cores than on 16
- **CPU**, as a percentage with the window it was measured over. **It reads `—` on the first poll after a restart** rather than `0`: `kern.cp_time` is cumulative and a percentage exists only between two readings (JD-031), and the card says so where the number would be
- **Memory**, used against physical, with wired, active and inactive spelled out underneath and a note that most of wired is the ZFS ARC — a single "used" figure on a ZFS host reads as pressure that is not there
- **Jails**, running of configured
- **Pools** — health, used, free, size and a capacity meter
- **Disks** — worst first, with the pool each one holds up, model, size, temperature, age and a health badge
- **Jails** — busiest first, with a CPU meter, memory, process count and open files

**A jail's CPU is shown in cores, not in `pcpu`'s own unit.** The API reports percent of *one* core, so a jail on two whole cores reads 200 — which beside a meter that is a share of all sixteen is the same number twice on two different bases. The table says `1.75 cores` and keeps the raw figure in the row's tooltip.

**A disk's badge is the worse of two disagreeing verdicts.** A drive reports its own overall assessment, and that verdict is not a summary of its attribute table: `isengard`'s `ada3` says `PASSED` while attribute 184 has been below threshold for days. The row reads `ATTENTION`, and a note under it names the attribute, its value against its threshold and the raw string smartctl printed — a green row over a failing attribute is how a disk gets ignored until it dies. Where the drive still claims `PASSED`, the note says so too, because an operator about to act on this deserves both halves (JD-032).

Disks are listed whether or not a pool stands on them, since a spare can fail too; those read `no pool`. Where smartctl needs a different path than `/dev/<device>` — an NVMe is `nda0` everywhere else but `/dev/nvme0` to smartctl — the row carries `read as /dev/nvme0`, so an operator reaching for the command line is not refused by it. A host without `smartmontools` gets a line saying the package is not installed, not an empty table.

The page polls every five seconds, which is also the window the host's CPU percentage is measured over, and the header carries a **Keep refreshing** checkbox — an operator reading a row should be able to stop it moving.

A jail that could not be read shows its reason in place of its figures; the rest of the page still renders. Nothing on this page is stored, and the footnote says where a chart over time will come from instead.

### Jails page

Core purpose and columns:

```text
Status | Name | JID | Hostname | IP | Path | Actions
```

Actions: View, Start, Stop, Restart, Create (opens the jail-creation flow). Action availability reflects current state (running: Stop/Restart/View; stopped: Start/View; unknown: View/Refresh).

**Create a jail** sits in the page header and goes to `/jails/new`. A jail really called `new` would be shadowed by that route; nothing stops one being created, and the alternative — reserving the name in the API because of a URL in the interface — is the wrong way round.

### Jail creation flow

Purpose: turn the clone-configure-start sequence (`docs/SPEC.md`) into a guided flow.

Built at `/jails/new`, **as one form rather than a wizard**. A wizard suits a flow whose later questions depend on the earlier answers; this one's do not, and paging a dozen fields across four screens would hide from an operator the thing they most want to see — the whole jail they are about to make. The form is sectioned instead: Source, Network, After it is built, and then **Limits**, **Jail parameters** and **Datasets and tags**, which open only when asked for. A jail that needs none of them reads as one that needs none, not as one where three sections were skipped.

- **Source** — the template select offers only ready ones; a template still being prepared is named underneath with its state, so a half-built template explains why it is not on offer rather than being silently absent. With no ready template at all, the form says so and links to Templates.
- **Network** — the mode decides which fields exist, because a field set for a mode that ignores it is refused by the API rather than dropped (JD-017). `inherit` carries the warning that it shares the host's `127.0.0.1`; `vnet` carries the measured truth about `private` — it isolates a jail from the *other private members*, not from everything on the bridge.
- **Limits** and **Jail parameters** are the allowlists made visible: the fields that exist are the ones the API will accept, so the interface and the refusal agree.
- **Datasets and tags** — repeatable rows, each dataset with the five native properties of JD-023.

**The result is the create's own report**, rendered as the steps the server ran in the order it ran them: clone, datasets, configure, autostart, start, each marked done, failed or not reached. A create that stopped half way shows exactly how far it got, because nothing is undone and that report is the recovery information (JD-030). A leaked epair that was cleaned up, and rctl rules taken back after a failed start, are named where they happened.

The one value the form will not quietly discard is a mistyped limit: `Number('abc')` is `NaN`, which JSON writes as `null` and the server reads as "no limit asked for", so a non-number is sent as typed and refused by name instead.

### Jail detail page

At `/jails/{name}`, reached from the jail's name on the list (JD-028). Built:

- header — name, status badge, JID, hostname, IP
- **Datasets** — every dataset of the jail, the one mounted at the jail's own path marked as such, with mountpoint, used/referenced/available (JD-022), and the native ZFS properties it sets itself (JD-023)
- **Tags** — the jail's `user:property` ZFS tags, its own and never a child's
- **Snapshots** — newest first, ordered by transaction group rather than by time (JD-024); fetched separately and allowed to fail on its own

Acting on it (JD-029): a tag can be added, edited and removed; a child dataset can be created with its native properties, or retained past the jail; a snapshot can be taken, rolled back to, or destroyed. Each opens a form where the thing is, or a confirmation when it cannot be undone, and each refetches the jail when it is done.

A refused snapshot operation is shown under its row, in the warning colour, naming what stood in the way. A rollback refused over newer snapshots offers them back once — "destroy them and roll back" — because that is the one refusal the API lets a client accept.

The jail as a whole: it can be **cloned as a template**, its **root re-cloned** from a ready template's snapshot, and it can be **destroyed**. A re-clone that fails part way through is rendered as the recovery procedure it is — which step stopped, where each dataset now sits, and that a restore brings it back.

**Destroy** is its own section at the foot of the page, in the danger colour: marked rather than hidden, because an operator looking for it should find it and should not reach it by accident. The confirmation names the child datasets that will go with the jail and asks for the jail's name to be typed, which is the API's own requirement shown rather than invented ceremony. A refusal over snapshots, clones or holds stays on the page with each one named — they have to be dealt with one at a time, so a message that disappears is no use. A jail with no dataset of its own cannot be destroyed here at all, and the section says so instead of offering a button that would always fail.

Still to come: services, recent logs, recent operations, and which template the jail was cloned from.

### Storage page

Purpose: inspect ZFS storage relevant to jails. Since ZFS is required (JD-006), this page doesn't need a "no ZFS" empty state — but should still handle "no datasets under management yet" gracefully. Content: datasets, mountpoints, used/available space, snapshots.

### Templates page

Purpose: the userlands a jail is cloned from — the thing jail creation depends on. Built, at `/templates`:

- the templates as an **indented lineage**, parent before child, each one ready or not ready, with what it was derived from and what it costs (JD-028)
- **Retained datasets** below: the datasets that outlived their jail (JD-025), on the same page because they answer the same question
- a page per template at `/templates/{name}`: where it lives, and **what depends on it** — the jails cloned from it, the templates derived from it, anything else, and holds — which is what a removal would have to refuse over

Acting on it (JD-029): a template can be **derived** from a ready one with packages, and **removed** — from the list or from its own page, which navigates back when the template is gone. A removal refused over clones or holds is shown where the template is, with the `zfs promote` way out. A **retained dataset can be restored** under any jail, under any name.

Preparing a release is there: a form for the release, a **State** column, and the running step with its elapsed time beneath it. The page polls while anything is in flight and stops when nothing is. A release is prepared end to end from here — fetched, unpacked, brought to its patch level and sealed — with the stage and its elapsed time visible throughout. Still open: a release picker sourced from `download.freebsd.org` or manual version entry, depending on how "which releases are supported" resolves (`docs/SPEC.md`).

### Logs page

Purpose: recent Jail Deck operation results, command failures, relevant jail log snippets where feasible.

### Settings page

Purpose: expose safe configuration visibility — HTTP bind address, detected FreeBSD paths, detected ZFS pool/dataset root, command paths, application version. Avoid dangerous editable settings.

## Frontend architecture

Not designed yet:

1. **Framework mode** — SvelteKit (file-based routing, more structure/conventions) or a plain Svelte + Vite SPA (fewer conventions, hand-rolled routing)? This decides several of the questions below.
2. **State management** — Svelte's built-in stores (`svelte/store`), Svelte 5 runes, or something else?
3. **Routing** — SvelteKit's built-in routing, or a lightweight router (`svelte-routing`, `page.js`) for a plain Svelte + Vite app; whether the route structure mirrors the API path structure from `docs/ARCHITECTURE.md`
4. **Component library / styling approach** — plain CSS, a utility framework, or a component library?
5. **API client pattern** — hand-rolled `fetch` wrappers, a generated client, or something like TanStack Query (`@tanstack/svelte-query`) for caching/refetch?
6. **Refresh pattern** — how the UI reflects state after a mutation: polling, optimistic updates, or plain refetch-after-mutation?
7. **Long-running operation UI** — JD-008 settled the API half (2026-10-23): the prepare call returns `202` and the template's own resource carries `state` and `running`. What the page does with that — a progress row on the template, a toast on completion, how often it polls — is still open.
8. **Build tooling** — Vite, either standalone or via SvelteKit (which is itself Vite-based) — the specific setup depends on the framework-mode decision above.

These should be settled together when Phase 1b (`docs/ROADMAP.md`) actually starts, not decided piecemeal ahead of time.

## States

Every major section should handle loading, empty, success, warning, error, unsupported. This applies to multi-step flows (jail creation, template preparation) at the step level, not just the page level.

## Empty states

Should explain what is happening:

- "No running jails were found."
- "No recent operation history is available yet."
- "This jail is stopped, so service status cannot be inspected."
- "No templates are ready yet — prepare one before creating a jail."
- "No datasets found under the jails storage root."

## Error states

Should show what operation failed, a short explanation, relevant command output when safe, and a next suggested action when obvious. Avoid generic messages like "Something went wrong." For multi-step flows, show *which step* failed, not just that the overall flow failed.

## Confirmation behavior

Potentially destructive or disruptive operations require confirmation: stop jail, restart jail, rollback snapshot, delete snapshot, remove dataset.

One modal dialog, mounted once in the shell and driven from anywhere (JD-029). It replaces the browser's `confirm()`, which could not carry the typed confirmation several of these operations need — the API asks for the full `dataset@snapshot` before it will destroy or roll one back (JD-024), and the dialog asks for exactly that.

Operations that destroy nothing — setting a tag, creating a dataset, retaining one — get a form in place instead. A modal for every change would make the dialog mean nothing.

## Accessibility and usability

Real buttons, real links, visible focus states, readable contrast, form labels, meaningful table headers, status text in addition to color.

## Copywriting guidelines

Use FreeBSD-native terminology, explain risky operations in plain language. Good: "Start jail", "Create jail from template", "ZFS is not available on this host." Avoid vague wording: "Launch container", "Destroy environment", "Unknown error."

## Open UI questions

1. ~~Table-first or card-first for the Jails page?~~ **Table-first**, with a page per jail behind the name (JD-028).
2. ~~Browser `confirm()` dialogs or custom confirmation components?~~ **One custom dialog**, because `confirm()` cannot ask for a typed confirmation (JD-029).
3. Should logs appear inline on jail detail or only on a dedicated logs page?
4. Should the dashboard be meaningful from the start, or should `/` redirect to `/jails`?
5. How much explanatory text for FreeBSD learners?
6. Dark mode: early or postponed?
7. Collapsible or fixed sidebar?
8. Wizard-style or single-form jail creation?
9. How is template preparation progress shown, given it can take real time?
10. ~~Does the Templates page need its own space, or does it live as a section of Storage?~~ **Its own page**, which also carries the retained datasets (JD-028). Whether a Storage page is still wanted once a jail's own page shows its datasets is now the open question.
