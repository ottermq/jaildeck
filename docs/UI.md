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

Built: **Dashboard**, **Instances**, **Jails**, **Templates**, **VMs**, **Networks**, **Operations**, **Firewall**, **Host**. A section stays highlighted on its detail pages, so `/jails/ottermq` keeps Jails lit.

**The version** sits under the logo, small and muted, from `GET /api/about` (#136): a release (`v0.19.0`) links to its notes, any other build is only named. It is there on every page and takes room from none; it is not beside "Signed in", which is about the session, not the software. On a narrow screen it sits beside the logo. The **Host** page has a **Reclaimable storage** section after the readiness report (#125, JD-049): what nothing uses, grouped by kind with each kind's size and the total, and an age in days for snapshots. Each row has **Remove**; templates, images and snapshots also have **Remove all N**, behind one confirmation that names every item and the space it gives back. Retained datasets have no Remove all, since each was kept on purpose. Archives have no button. Removals go one at a time, and a refusal is shown on its own row while the rest go on. The snapshots another tool prunes are only counted, in a line under the list. The **Host** page ends with a **Jail Deck** block for when something needs debugging: the version, the commit it was built from (and whether that tree had changes), when it started, and its state directory.

On a narrow screen the sections become a row across the top, and nine of them are wider than a phone: the row scrolls sideways on its own, and the page does not. Measured at 390px wide: the six sections before VMs already took 486px of links, and the rail, sized to them, made the whole page scroll sideways.

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

### Instances page

Purpose: jails and guests in one list, at dozens and later hundreds, and acting on several at once (#129). Built at `/instances`.

**One row per instance**: a checkbox, state, kind, name (linked to its own page), labels, CPU and memory. **CPU and memory mean different things for the two kinds, and the headers say so on hover**: a running jail's are what it is using, from the metrics report (percent of one core, memory in use); a guest's are what it was given (vCPUs, configured memory). A stopped jail has neither. A failed metrics read blanks the jails' usage and says so, without hiding the list.

**Filters**: a name search, kind, state (the states present), labels, and **Group by** a label key. Labels are comma-separated terms that must all hold — `key=value` for that value, a bare `key` for any — commas because a ZFS value may contain a space. Clicking a label in a row adds it to the filter. Grouping puts each value under its own heading with a count and a checkbox for the group, and the instances without the key last. Name, kind and state sort from their headers. **Every filter is in the address** (`?kind=vm&label=otter:tenant%3Dacme&group=otter:tier`), replaced rather than pushed so Back is not a walk through keystrokes, and only what differs from the defaults is written.

**Bulk actions** — Start, Stop, Restart, Snapshot — sit in a bar that appears with a selection and stays at the top while scrolling. **A bulk action is the existing per-instance call made once per instance**, a few at a time (four: a guest's stop can take its minute), each audited as it always is; nothing new on the server. Before it runs:

- **What does not apply is left out, per instance and with the reason**: a start skips what is running, a stop and a restart what is not, a guest in its boot loader or held by another host is skipped for what it is doing.
- **One confirmation for the set**, naming each instance it will act on and its kind, with the stop deadline when guests are among them and how many selected instances are left alone.
- **What a filter hides leaves the selection**, so nothing is stopped that was not on screen when Stop was pressed.

**Each instance's result is reported separately**, as it finishes, in a panel above the list: queued, running, done, failed with the API's words, or skipped with the reason. A guest powered off after its minute is done in amber, saying so. One failing does not hide the others. The list is read again afterwards, however it went.

**Snapshot** asks for a name (it starts as `jd-YYYYMMDD-HHMM`). A jail's is its recursive snapshot, the jail page's own call. A guest's is its dataset and its disks (JD-045), **taken in any state and said to be what it is**: the confirmation says a running guest is taken crash-consistent, and each guest's result says "Crash-consistent: taken while the guest ran" or "Consistent: the guest was stopped". An instance that already has the snapshot is reported and left alone.

### Jails page

Core purpose and columns:

```text
Status | Name | JID | Hostname | IP | Path | Actions
```

Actions: View, Start, Stop, Restart, Create (opens the jail-creation flow). Action availability reflects current state (running: Stop/Restart/View; stopped: Start/View; unknown: View/Refresh).

**Create a jail** sits in the page header and goes to `/jails/new`. A jail really called `new` would be shadowed by that route; nothing stops one being created, and the alternative — reserving the name in the API because of a URL in the interface — is the wrong way round.

### Jail creation flow

**The name starts as a suggestion** (#148), as it does in the Create a VM form: an adjective and an animal, `jolly-otter`, free on the host when drawn, from `GET /api/names/suggest`. **Another** beside the field draws a new one. Typing replaces it, and a suggestion still on its way when typing starts doesn't overwrite what was typed. No suggestion at all (the server unreachable) leaves the field empty, as before.

Purpose: turn the clone-configure-start sequence (`docs/SPEC.md`) into a guided flow.

Built at `/jails/new`, **as one form rather than a wizard**. A wizard suits a flow whose later questions depend on the earlier answers; this one's do not, and paging a dozen fields across four screens would hide from an operator the thing they most want to see — the whole jail they are about to make. The form is sectioned instead: Source, Network, After it is built, and then **Limits**, **Jail parameters** and **Datasets and tags**, which open only when asked for. A jail that needs none of them reads as one that needs none, not as one where three sections were skipped.

- **Source** — the template select offers only ready ones; a template still being prepared is named underneath with its state, so a half-built template explains why it is not on offer rather than being silently absent. With no ready template at all, the form says so and links to Templates.
- **Network** — the mode decides which fields exist, because a field set for a mode that ignores it is refused by the API rather than dropped (JD-017). `inherit` carries the warning that it shares the host's `127.0.0.1`; `vnet` carries the measured truth about `private` — it isolates a jail from the *other private members*, not from everything on the bridge. **On** lists Jail Deck's networks first, then "another bridge" (JD-047). On one of its networks, the jail is sent by the network's name, the address placeholder says "picked by Jail Deck", and the gateway placeholder is the network's: left empty, both are the server's to fill. The report then says which address it got, on which network.
- **Limits** and **Jail parameters** are the allowlists made visible: the fields that exist are the ones the API will accept, so the interface and the refusal agree.
- **Datasets and tags** — repeatable rows, each dataset with the five native properties of JD-023.

**The result is the create's own report**, rendered as the steps the server ran in the order it ran them: clone, datasets, configure, autostart, start, each marked done, failed or not reached. A create that stopped half way shows exactly how far it got, because nothing is undone and that report is the recovery information (JD-030). A leaked epair that was cleaned up, and rctl rules taken back after a failed start, are named where they happened.

The one value the form will not quietly discard is a mistyped limit: `Number('abc')` is `NaN`, which JSON writes as `null` and the server reads as "no limit asked for", so a non-number is sent as typed and refused by name instead.

### Jail detail page

At `/jails/{name}`, reached from the jail's name on the list (JD-028). Built:

- header — name, status badge, JID, hostname, IP
- **Console** — while the jail runs (JD-042): **Open a shell** puts a terminal (xterm.js) on the page, sized to it, attached to `jexec -l <jail> /bin/sh -l` over a WebSocket. It stays as long as the shell does. When it closes, the reason is said in words, and the terminal stays visible, dimmed. "The jail stopped" also re-reads the jail, because the stop may have come from elsewhere. **Open again** appears only while the jail still runs. The hint under it says that the session is audited, that **nothing typed or shown is recorded**, and what closes it. **On a host with the browser console turned off** (`JAILDECK_CONSOLE_UI=off`, #155), the login's `scopes` from `/api/auth/me` lack `console`: the panel offers no button and says the console is off on this host and that a platform opens consoles through its own token. The page doesn't just hide it, because a missing button reads as a bug. Measured in headless Chrome: the fit gives `stty size` the panel's own size, and at 390px the page doesn't scroll sideways.
- **Datasets** — every dataset of the jail, the one mounted at the jail's own path marked as such, with mountpoint, used/referenced/available (JD-022), and the native ZFS properties it sets itself (JD-023)
- **Tags** — the jail's `user:property` ZFS tags, its own and never a child's
- **Inspect** — the jail's own `jail.conf.d` file and its rctl rules as they are on the host now (#127, JD-048). Closed until **Show the files**, so the page reads nothing more; open, **Reload** reads the host again. Each file is under its path with a Copy button. A jail with no file of its own says it is configured somewhere Jail Deck doesn't write, most likely `/etc/jail.conf`, which isn't shown. No rules, and rules that can't be read, are each said in words. The component (`InspectPanel`) is the guest page's too.
- **Snapshots** — newest first, ordered by transaction group rather than by time (JD-024); fetched separately and allowed to fail on its own

Acting on it (JD-029): a tag can be added, edited and removed; a child dataset can be created with its native properties, retained past the jail, or destroyed with its snapshots while the jail is stopped (#120); a snapshot can be taken, rolled back to, or destroyed. Each opens a form where the thing is, or a confirmation when it cannot be undone, and each refetches the jail when it is done.

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

Acting on it (JD-029): a template can be **derived** from a ready one with packages, and **removed** — from the list or from its own page, which navigates back when the template is gone. A removal refused over clones or holds is shown where the template is, with the `zfs promote` way out. A **retained dataset can be restored** under any jail, under any name, or **destroyed** with its snapshots, which the confirmation lists first; a destroy refused over a hold, a clone or a dataset of its own is shown where the dataset is (#120).

Preparing a release is there: a form for the release, a **State** column, and the running step with its elapsed time beneath it. The page polls while anything is in flight and stops when nothing is. A release is prepared end to end from here — fetched, unpacked, brought to its patch level and sealed — with the stage and its elapsed time visible throughout. Still open: a release picker sourced from `download.freebsd.org` or manual version entry, depending on how "which releases are supported" resolves (`docs/SPEC.md`).

### Firewall page

Purpose: what PF is doing, and nothing that could change it (JD-020, JD-034). Built at `/firewall`.

**The state comes first**, because the rest of the page means different things in each:

- **Not loaded** — the common case on the reference hosts. One plain line saying PF is filtering nothing, and that turning it on is the operator's `pf.conf` and `pf_enable`. Not an error, and not styled as one.
- **Disabled** — an amber banner: a ruleset is loaded and *none of it is being applied*. The rules below read exactly as they would if they were, which is why this is said before them.
- **Enabled** — a badge and how long for.

**The duration is pfctl's own, said honestly.** pfctl's clock restarts at every enable and disable, and counts from boot when neither has happened — a PF loaded and never enabled reports the host's uptime as "disabled for". So a disabled PF reads "pfctl's clock says 21 min — since it was last switched off, or since boot if it has never been on", not "disabled for 21 min".

Then four counters (states, states created, rule matches, state mismatches), and the policy:

- **Rules** — pfctl's text as pfctl printed it, with the action pulled out in front only to be coloured, and per-rule evaluations, packets, bytes and states. A rule nothing has matched is greyed, not hidden: quiet is not wrong. The header reminds that the last match wins unless `quick`.
- **NAT** — listed apart, because `pfctl -s rules` does not show it, and only when there is any.
- **Anchors** — each with its own rules; an anchor's contents are not part of the main listing, which shows only `anchor "jaildeck/*" all`. Empty anchors (a wildcard's parent, usually) are listed last as "Empty". One that could not be read shows why.
- **Tables** — name and flags, spelled out (persistent · active · used by a rule). **No address count**: pfctl's is wrong on 15.1. *Show addresses* reads the table when it is opened, because a blocklist can hold a hundred thousand entries.
- **Recent log** — newest first, with the rule a line refers to resolved by number. A line from inside an anchor says "in an anchor named `web`", because the last component of the name is all pflog keeps. A footnote says the file can be a minute behind (pflogd's flush, which Jail Deck does not trigger) and that `log` records the packet that opens a connection, not every packet. A log too large to read to its end shows no lines and says why, rather than its oldest lines under the word "recent".

A **Refresh** button rather than polling: a policy changes when someone reloads it, not every five seconds.

### Virtual machines

Purpose: what vm-bhyve's guests are doing, and starting and stopping them (JD-019, JD-035, JD-036). Built at `/vms` and `/vms/{name}`.

**vm-bhyve's own state comes first**, as PF's does on the Firewall page:

- **Not installed** — the common case. One plain line, and a link to Host Setup, which says what a host needs. Not an error.
- **Not configured** — amber, with vm-bhyve's own message quoted (`$vm_enable is not enabled in /etc/rc.conf!`) and a link to Host Setup for the remedy.
- **Ready with no guests** — said in those words.

The list is a table: state, name, CPU, memory, loader, datastore, and whether the guest starts at boot with its place in `vm_list`. Only `running` is green. `bootloader` and `locked` are amber, and a locked guest names the host holding it. CPU and memory the API could not read are a dash.

A guest's page has its configuration (with resident memory beside the configured amount while it runs, and the host CPUs it is pinned to beside its CPU count), its console — **Open the console** while it runs, the same panel as a jail's (`ConsolePanel`), with the device it attaches to, and the two things an operator must know: one console at a time here while `vm console` can still attach alongside, and closing it logs no one out of the guest — its network interfaces, its disks and its **Tags**. **A stopped guest's missing console is explained, not left blank**: the console belongs to the bhyve instance. The tap is labelled as this run's, because vm-bhyve makes a new one on every start.

**Tags** are the jail page's panel, one component (`TagsPanel`) for both: the ZFS user properties on the guest's dataset (JD-044), added, edited and removed in place, with a confirmation before a remove. The hint says where they live, so that a rename keeps them and a destroy takes them. A guest in a datastore that isn't on ZFS has its refusal shown in the form.

**Inspect** is the jail page's panel too (#127, JD-048): the guest's `<name>.conf` and its seed's `meta-data`, `user-data` and `network-config` as they are on the host. A console password's hash is masked, and the panel says so and why. A seed file that isn't there says what that means: no `network-config` is DHCP or no network, no seed at all is a guest made without one.

**Start, Stop and Restart** sit at the end of each row and beside the guest's name on its page — one component, `VMActions`, for both. A running guest offers Stop and Restart, a stopped one Start, and a suspended one **Resume**, which is what vm-bhyve's start does to it. A guest in its boot loader or locked by another host offers nothing and says why ("Booting — try again once it is running", "Held by orthanc"), because the API would refuse.

**Stop and Restart ask first, and the question is the deadline:** the guest gets the power button and a minute, and after that it is powered off and loses what it had not written. Start does not ask — nothing is lost by starting. A stop that came to a poweroff (`forced`) says so under the row, in amber, and a failure says vm-bhyve's words in red. Either way the page reads vm-bhyve again afterwards, because a failed operation can still have changed the guest.

**Images** are a second panel under the guests, shown once vm-bhyve is ready (JD-037): each image's state, the source it was written from with the first twelve characters of its build's SHA-512 (the whole one on hover), its disk size, what it costs the pool, and the guests cloned from it, linked to their pages. A panel rather than a page of its own, because `/vms/images` would be the page of a guest called `images`. A datastore that cannot hold images — not on ZFS — gets amber and the server's reason, and no Prepare button.

**Preparing** is a form with the source and a name that starts as the source's own. The hint says what will happen: the URL to be downloaded and checked, or, when the source's archive is already on the host, that this is only a write. The panel then polls every two seconds while anything is running and stops when nothing is, as Templates does (JD-008). **It says how the work it started came out**: "ready", or, in amber, that the image stopped before it was ready and at which state, with a link to Operations, where the failed stage's audit entry says why. Without that, a failed write looked exactly like a successful start until someone noticed the grey badge — the screenshots showed it.

**Removing** asks for the name typed, as removing a template does. The refusal for an image a guest uses comes back naming the guest and is shown under the image's row.

**Create a VM** sits beside the Guests heading once vm-bhyve is ready (JD-038). The form offers only ready images, with the image's size as the disk's placeholder and the floor the hint names. **Network** is a choice of vm-bhyve's switches — the listing now carries them — or "no network"; with no switches at all, the hint says a guest made now has none and that switches are the operator's. With a switch, the address is DHCP or static, and static opens address, gateway and nameservers. SSH keys are one per line. **Console password** is optional, a password field, for the same user on the guest's console only; its hint says that a cloud image has no password of its own, so without one the console stops at the login prompt, and **without a key or a password there is no way in** (#152). It is sent as typed, spaces kept. **Pin to CPUs** is optional: host CPU numbers, one for each vCPU, `2,3` (JD-041). Anything but numbers is refused in the form, with what a pin is; the server checks the count and that the host has them. Start is ticked by default. A refusal stays in the form; success closes it, says what was made and that a cloud image takes about twenty seconds to boot, and reads both the guests and the images panel again, since the image now has a guest.

**Default network** is a third panel, between the guests and the images (JD-040). One line says what it is — switch, subnet, gateway, the interface it goes out through — under a badge: **ready** (green) when every part passes, **host only** (amber) when guests reach the host and not yet the outside, **not set up** otherwise. Below it, every part that doesn't pass, with its **owner**: "Jail Deck" or "you". An operator's part carries its remedy in a block with a Copy button: the PF commands, with the warning that enabling PF over SSH can drop that session, and the three `pf.conf` lines. **Set up** appears only while one of Jail Deck's own parts fails, and the hint under it names what it does and that PF and `pf.conf` are left alone. Done, it says which parts it put in place and, after making the ports private, that a guest already running keeps its shared port until it restarts. Once ready, the parts fold behind "Show the parts".

**On one of Jail Deck's networks** (the default one or a tenant one, listed first as "on network <name>"), the address defaults to **Picked by Jail Deck**, and only the nameservers are sent; static stays on offer for an address the operator wants (JD-047). A guest's page has a **Connect to** row with the address from its seed.

**On the default network, the create form changes**: it's preselected and listed first as "on the default network (jaildeck)", DHCP isn't offered because there's no DHCP server on it, the gateway is filled in, the address placeholder is in its subnet, and the hint says what a guest on it reaches.

**Destroy** is on a guest's page, beside Start, **only while it is stopped or suspended** — stopping is its own decision with its own deadline — and asks for the name typed, saying the disk and everything on it go and the image does not. Done, it goes back to the list; refused, it says why and stays.

Checked against the real host as well as the fake: on isengard through an SSH tunnel, a guest created from the form was reached over SSH on the address typed into it, then stopped and destroyed from its page.

No polling: a guest changes when someone starts or stops it, and the page re-reads after its own operations.

### Networks page

Built at `/networks` (#128), from `GET /api/topology`: every bridge on the host and every vm-bhyve switch, each as a section with its kind (the default network, a tenant network, or the operator's own), its switch, its addresses, whether Jail Deck's NAT rules translate it, and a table of what is on it. A jail's epair and a guest's tap link to the jail's or the guest's page; any other member, a physical uplink for one, is listed as not an instance's. A switch whose bridge does not exist yet says vm-bhyve makes it at `vm init`. A Jail Deck network's bridge has an **In use** list: each address, with the jail or guest holding it, or "seen on the bridge, in no configuration" for one only the ARP table knows (JD-047).

Read only, and read off the host on every visit (JD-011): creating a tenant network stays on the API (`POST /api/networks`). The endpoint is `/api/topology` rather than under `/api/networks`, where a tenant network named "topology" would be hidden behind it. NAT is "loaded" only from the `jaildeck/*` anchors' rules; the operator's own `pf.conf` is not read, so "no NAT" means Jail Deck's rules do not translate it.

### Logs page

Purpose: recent Jail Deck operation results, command failures, relevant jail log snippets where feasible.

### Settings page

Purpose: expose safe configuration visibility — HTTP bind address, detected FreeBSD paths, detected ZFS pool/dataset root, command paths, application version. Avoid dangerous editable settings.

## Frontend architecture

Settled by building it, between v0.6.0 and v0.16.0. These were open questions here until then; what follows is what was built and why it stayed.

1. **Plain Svelte 5 + Vite, not SvelteKit.** It's a single-page application with no server rendering, built by `make web` into `web/dist` and embedded into the Go binary (`web/embed.go`, JD-009). It has **no runtime dependencies at all**: everything in `package.json` is a devDependency, and production ships one executable. That still holds with xterm.js (JD-042), which is bundled like the rest. It's loaded as a chunk of its own (about 330 KB) only when a console opens, so no other page pays for it.
2. **State is runes in modules.** Each domain has a store module, `src/lib/stores/<domain>.svelte.ts`, holding its types and the API calls. Pages keep their own `$state` and `$derived`. There's no global cache. Every page reads what it shows when it mounts, because the host is the record (JD-011).
3. **A router of a few dozen lines** (`src/lib/router.svelte.ts`): a `$state` path, `match()` for `:name` segments, `navigate()` and `onNavClick()`. A dependency would bring history handling, lazy loading and nested layouts this application has no use for. Paths mirror the API's where they can (`/jails/{name}`, `/vms/{name}`), and two pages must never claim the same path. That's why VM images are a panel on the VMs page and not `/vms/images`, which would be a guest called `images`.
4. **Plain CSS.** Design tokens are custom properties in `src/app.css`, redefined under `prefers-color-scheme: dark`. Global styles cover the controls, tables, badges and notices. Each component's own layout is scoped in its `<style>`. There's no utility framework and no component library.
5. **One `fetch` wrapper** (`src/lib/services/api.ts`). Every call goes to `/api`, and a `401` clears the session state, so the app shows the login page. A failure is an `ApiError` carrying the status, the server's message and the decoded body, because a `409` often explains itself in its body (JD-024).
6. **Refetch after every mutation**, whichever way it went: a command that failed can still have changed the host. Nothing is optimistic.
7. **Long-running work is polled.** A page reads its listing every 2 seconds while anything in it has `running` (JD-008), and stops when nothing does. Templates and VM images both work this way. A page that started work says how it came out, because a `202` only says the work began.
8. **Destructive actions go through one confirm dialog** (`stores/confirm.svelte.ts`, mounted once in the shell). It asks for a typed name where the API demands `confirm=`.

## Checking a frontend change

**A frontend change is not verified until someone has looked at it**, in a browser, in the states it can be in. Two of the four bugs v0.9.0's interface shipped were layout — a `<legend>` drawn through its own fieldset's border, a section `gap` that did nothing — and had no textual symptom at all: the markup and the CSS were each correct on their own. No test here would have caught them, and visual-regression tooling is not worth its upkeep at this size. Looking is the check, and the fake adapter (`make run-fake`) exists so it can be done on any machine.

What *can* be checked by a program is, and `make web-check` runs it — the type check and the tests together, because the type check alone passed every bug that release shipped (#77):

- **Vitest, with `@testing-library/svelte` in jsdom.** A test renders the real component, uses it the way a person does — types into a field, adds rows, removes one — and asserts what it sends. jsdom rather than a browser, because what these check is what a control binds and what a form submits, which needs no layout.
- **`buildSpec` is a module, not a component's private function** (`src/lib/jailSpec.ts`). It is the one place a control's value becomes what the API takes, and v0.9.0's `NaN`-to-unlimited bug lived there.
- **What a binding gives is the thing TypeScript cannot see.** A number input binds a number, and an emptied one binds `null`; `svelte-check` accepts a `Record<string, string>` bound to it. The tests put each v0.9.0 bug back and fail on it.
- **Keyed rows: what index keys break is element identity, not values.** Measured: with every field in a row bound, keying by index still shows and sends the right values — Svelte 5 re-points a reused element's binding at the row now in its place. What moves is the element: remove the first of three rows and the input that held row b holds row c, so a person's focus, caret or half-typed word jumps rows. The test asserts identity, because that is what breaks.

A page whose meaning depends on the server's state — the Firewall page's three PF states, the VM pages' three vm-bhyve states — gets a test per state, against a mocked store, for what each state *says*. How it looks is still checked by looking.

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
