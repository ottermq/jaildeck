# Changelog

Every released version is recorded here. A version additionally gets a
[release](https://github.com/ottermq/jaildeck/releases) with full notes when
it carries something an upgrader has to act on — a changed output format, a new
mandatory step, a migration. Versions that only fix internal behaviour live here
alone.

Dates are the tag date. Issue numbers link to
[the tracker](https://github.com/ottermq/jaildeck/issues).

Anything that changes the JSON API's surface is recorded under an **API** heading,
whether or not it breaks a client, so that a client pinning a version can see in
one place what a contract test would notice (JD-016). Breaking changes are called
out as such; before 1.0 they are allowed, but never silent.

The repository moved from Codeberg to GitHub on 2026-10-21. Issue and pull
request numbers in v0.6.0 and earlier, including the audit cursor entry in
v0.7.0, refer to the archived
[Codeberg tracker](https://codeberg.org/Otter-Labs/jaildeck/issues), and those
versions' release notes stay there. GitHub numbering started again at #1.

## Unreleased

### Added

- **Inspect: an instance's files as the host has them** (#127, JD-048). An **Inspect** panel on a
  jail's page shows its own `jail.conf.d` stanza and its rctl rules; on a guest's page, its
  vm-bhyve configuration and cloud-init seed. Read off the host when the panel is opened, read
  only, each file with a Copy button. **A console password's hash is masked.**

### Fixed

- **A guest's directory could be read by any local account** (#152). Its seed's `user-data` is
  0600, but `vm create` leaves the directory 0755 and the first start writes the seed's ISO into
  it 0644, so a console password's hash could be read from the ISO. **A create now makes the
  directory 0700** before the guest can start, audited as `vm-make-private`; vm-bhyve keeps the
  mode, measured. **A guest made before the upgrade keeps its directory as it was:** run
  `chmod 0700 <datastore>/<guest>` for each one (`/vm/<guest>` on the default datastore).

### API

- **`GET /api/jails/{name}/configuration`**: `file` (`path`, `present`, `content`, `truncated`),
  `rules`, and `rules_error` when they could not be read. A jail configured outside
  `jail.conf.d` answers `present: false`. Additive.
- **`GET /api/vms/{name}/configuration`**: `files`, the configuration and the seed's three files,
  each with `name`, `path`, `present`, `content`, `truncated` and `masked`. The value of every
  `password`, `passwd`, `hashed_passwd` and `plain_text_passwd` key is replaced with `(masked)`.
  A file past 64 KiB is cut there. Additive.
- **`POST` and `PUT /api/vms`** run one more step, `vm-make-private`, right after `vm-create`; a
  failure there destroys the guest again, as a later step's does.

### Internal

- **The measurements are kept as a capture:** `capture-vm-configuration-induced.sh` (`vcf_*`),
  what a guest's directory holds and with what modes. **JD-048** records the view.
- `capture-vm-private-dir-induced.sh` (`vpd_*`): what a plain account reads of a guest's directory,
  before and after it is made private, and that vm-bhyve keeps the mode. JD-038's amendment of
  2026-10-30 records it.

## v0.24.0 — 2026-10-30

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.24.0)

**Addresses on Jail Deck's networks.** A jail or guest created on the default network or a tenant
network no longer needs an address: Jail Deck picks the lowest free one and refuses one that is
taken, naming who has it (#118). A jail can be created on a network by its name, and each network
shows the addresses in use.

Nothing to act on when upgrading: no new setting, and nothing in `pf.conf` or `rc.conf`. **A jail
spec whose `ip4`, `gateway`, `interface` or `bridge` is not shaped as one is now refused with 400**,
where it used to be written into the stanza.

### Added

- **Jail Deck picks a member's address on its own networks** (#118, JD-047). A jail or guest
  created on the default network or a tenant network with no address gets the lowest free one,
  and the network's gateway. What is taken is read off the host at every create: every jail
  stanza, every guest's seed, the gateway and the bridge's ARP table. Creates on one network
  run one at a time. An address asked for is checked: in the subnet, not the gateway, and
  free, or the create is refused with the holder named.
- **The interface**: the jail form offers Jail Deck's networks under **On**, and the address is
  left to it; the guest form's addressing defaults to "picked by Jail Deck" on them. A guest's
  page says **Connect to**, and the Networks page lists each network's addresses in use.

### Fixed

- **A jail spec's `ip4`, `gateway`, `interface` and `bridge` were written into the stanza
  unchecked.** They are now checked for their shape before anything runs, and a value
  carrying more stanza is refused with 400.

### API

- **`network.name`** in a jail spec, for mode `vnet`: a Jail Deck network by name, in place of
  `network.bridge`. Exactly one of the two.
- **`network.ip4` may be left out** for a VNET jail on a Jail Deck network, and
  `network.address` for a guest on one. Both creates answer `network` and `address`.
- **`addresses`** on `GET /api/networks`, `GET /api/networks/{name}` and
  `GET /api/networks/default`: each address in use, with its kind (`jail`, `guest`, `seen`) and
  holder; `null` when it could not be read. **`in_use`** on Jail Deck's bridges in
  `GET /api/topology`.
- **`address`** on a guest's detail, from its seed. A VNET jail's `ip` is now its stanza's
  address, where it was empty.
- **New refusals:** 409 for an address taken or a network full; 400 for an address outside the
  network, its gateway, network or broadcast address, a jail with both `bridge` and `name`, or
  an unknown network name; 501 for `network.name` where networks are not available.

### Internal

- **The measurements are kept as a capture:** `capture-network-addresses-induced.sh` (`nad_*`),
  showing that a quiet address is not in a bridge's ARP table. **JD-047** records the sources,
  the per-network lock and `network.name`.
- The fake host's jails, guests and networks read each other's addresses, so the demo picks
  them as a host would.

## v0.23.0 — 2026-10-30

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.23.0)

**Instances at scale.** Jails and guests are in one list, found by name, state or label, and
acted on several at once (#129). Guests get labels and snapshots, as jails have. API tokens get
scopes, and a console can be opened saying on whose behalf and why (#155, #178).

Nothing to act on when upgrading: existing tokens keep every scope, and the browser console stays
on. **A token made after the upgrade needs `--scope console` to open a console.** One new
optional setting, `JAILDECK_CONSOLE_UI`; nothing in `pf.conf` or `rc.conf`.

### Added

- **The Instances page** (#175, closing #129): `/instances`, second in the menu. Jails and guests
  in one list, with state, kind, labels, CPU and memory.
  - A running jail's CPU and memory are what it is using; a guest's are what it was given. The
    headers say so.
  - Search by name; filter by kind, state and labels (`otter:tenant=acme, otter:tier`); click a
    label to filter on it; group by a label key; sort by name, kind or state. **The filters are
    kept in the address**, so a view can be bookmarked.
- **Bulk start, stop, restart and snapshot** (#175): one confirmation that names each instance it
  will act on, four at a time, and **each instance's result shown separately** as it finishes.
  - What an action does not apply to is skipped, with the reason per instance.
  - What a filter hides leaves the selection, so nothing is acted on that was not on screen.
  - It is the existing per-instance call, made once per instance and audited as before.
- **Guest labels** (#173): ZFS user properties on the guest's dataset, as a jail is tagged
  (JD-044). A **Tags** panel on the guest's page, the jail page's own, now one component.
- **Guest snapshots** (#176, closing #174): the guest's dataset and its disks at one point in
  time. **A running guest is snapshotted as it is, and said to be crash-consistent** (JD-045).
  The Instances page's bulk Snapshot covers guests, each result saying which it got.

- **Token scopes** (#179, closing #178): `jaildeck token add <name> --scope console`. The one
  scope is `console`; a token without it is refused a console with 403. A token made before
  scopes keeps every one, and `token list` shows each token's scopes.
- **`JAILDECK_CONSOLE_UI=off`** (#179, #155): the browser session can no longer open a console,
  and a token with the `console` scope still can, for a host a platform manages. Anything but
  `on` or `off` is taken as off. The console panel says the console is off rather than hiding it.
- **Who opened a console, and why** (#179, #155): optional `actor` and `reason` on both console
  endpoints, recorded verbatim on the `console-open` audit entry. Never required.

### API

- **`properties`** on each guest in `GET /api/vms` and on `GET /api/vms/{name}` (#173): the
  guest dataset's own user properties, always an object. Additive.
- **`PATCH /api/vms/{name}/properties`** (#173): as the jail route, a string sets and `null`
  removes; `409` for a guest in a datastore not on ZFS. Additive.
- **`PUT /api/vms/{name}/snapshots/{snapshot}`** (#176): `201` with `snapshot`, `created` and
  `consistency` (`crash-consistent` or `consistent`); `200` with `existed` when it was already
  there, with no consistency claimed; `409` for a datastore not on ZFS. Additive.

- **`scopes`** on `GET /api/auth/me` (#179). Additive.
- **`actor` and `reason`** query parameters on `GET /api/jails/{name}/console` and
  `GET /api/vms/{name}/console` (#179): `400` when out of shape. **`403`** for a token without the
  `console` scope, or a browser session with `JAILDECK_CONSOLE_UI=off`. The audit's
  `console-open` entry gains `actor` and `open_reason`. Additive for existing tokens.

### Internal

- **The measurements are kept as captures:** `capture-vm-labels-induced.sh` (`vml_*`) and
  `capture-vm-snapshot-induced.sh` (`vsn_*`). **JD-044** (guest labels: ZFS over a config key that
  `vm info` does not show) and **JD-045** (guest snapshots) record them.
- The fake host's running guest has a `disk0` volume, as a real guest does.
- **JD-046** records token scopes, the console switch, and the actor and reason. The state file's
  tokens gain `scopes`, written as `[]` or a list for a new token and left `null` for an old one.

## v0.22.0 — 2026-10-30

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.22.0)

**The end of a retain.** A retained dataset can be destroyed with its snapshots, and so can one
of a jail's children without the jail (#120). Until now both ended at a root shell.

Nothing to act on when upgrading: no new setting, and nothing in `pf.conf` or `rc.conf`.

### Added

- **Destroying a retained dataset** (#170, closing #120): a **Destroy** button beside Restore
  under **Templates → Retained datasets**. The confirmation lists the snapshots that go with it
  and asks for the name.
- **Destroying a jail's child** (#170): a **Destroy** button on each child on the jail's page,
  disabled while the jail runs. The jail and its own dataset stay.
  - Both are refused, before anything runs, while a snapshot has a hold or a dependent clone, or
    while the dataset has datasets of its own, and the refusal names each.
  - A child is refused while its jail runs: zfs would unmount it from under the jail without
    complaint (measured).
  - The dataset is unmounted on its own before the destroy. `zfs destroy -r` refused as busy, a
    host process with a file open on it, has already destroyed the snapshots (measured); a busy
    unmount loses nothing.

### API

- **`DELETE /api/retained/{name}?confirm={name}`** and
  **`DELETE /api/jails/{name}/datasets/{child}?confirm={child}`** (#170). `200` with `dataset`,
  `snapshots` and `destroyed`; `409` with the same and `blocked` (`reason`, `children`, `clones`,
  `holds`), or for a child of a running jail; `404` once it is gone. Additive.
- **`snapshots`** on each entry of `GET /api/retained` (#170): what a destroy would take with
  it. Additive.

### Internal

- **The measurements are kept as captures:** `capture-destroy-dataset-induced.sh` with its
  `des_*` outputs. **JD-025 amended** with what was measured.

---

## v0.21.0 — 2026-10-29

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.21.0)

**Limits changed in place.** A jail's resource limits can be changed without recreating it
(#119): a running jail gets the change at once, and its stanza so the next start applies the
same. A packaged Jail Deck now says **which commit it was built from** (#165).

Nothing to act on when upgrading: no new setting, and nothing in `pf.conf` or `rc.conf`.

### Added

- **Changing an existing jail's limits** (#166, closing #119): `PATCH /api/jails/{name}/limits`.
  - Only the limits named change; `null` removes one. All of the create-time limits:
    `memoryuse`, `vmemoryuse`, `maxproc`, `openfiles`, `pcpu` and `cpuset`.
  - A running jail gets each change live, one audited `rctl -a`, `rctl -r` or `cpuset` at a time,
    and then the stanza's limit hooks are rewritten. A step the host refuses, or a stanza that
    cannot be written, takes back the steps already made, so the jail runs as it did.
  - A stopped jail gets the stanza only.
  - Lowering a limit below what the jail already uses is done and named in `warnings`, not
    refused: a lower `maxproc` keeps the processes that run and refuses the next fork.

### Fixed

- **A packaged build reported no commit** (#167, closing #165). The package build ran without
  git, so the Go toolchain stamped none, and `GET /api/about`, the Host page and
  `jaildeck_build_info` showed an empty commit. `make package` now injects it like the version
  (`COMMIT=`, taken from git when there is a repository), and the build fails if the binary
  disagrees.

### API

- **`PATCH /api/jails/{name}/limits`** (#166). Answers what the stanza and `rctl` say afterwards,
  `changed`, the rules applied and any `warnings`; the same request again answers
  `changed: false`. `400` for an unknown limit, a malformed one, or `0` and `""` (send `null`).
  `409` for a jail configured outside `jail.conf.d`, a stanza whose `exec.poststart` or
  `exec.prestop` carries a command Jail Deck did not write, and a stanza changed while it was
  being rewritten. Additive.

### Internal

- **`jaildeck version --commit`** prints the revision alone, and `package.sh` checks the built
  binary with it, as it already checks the version (#167).
- **The measurements are kept as captures:** `capture-limits-induced.sh` with its `lim_*`
  outputs. **JD-018 amended** with what was measured: every limit applies to a running jail at
  once, `rctl -a` replaces a deny rule, and a stanza's header now says which lines Jail Deck
  rewrites.

---

## v0.20.0 — 2026-10-29

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.20.0)

**A way in at the console, and a map of the networks.** A guest made from a cloud image can be
given a **console password** (#152), so its serial console gets past the login prompt while SSH
stays key-only. A read-only **Networks** page shows every bridge and switch on the host and what
is on each (#128). The interface says **which Jail Deck is running** (#136), and retaining a
jail's child dataset that has its **own mountpoint** no longer leaves it mounted in the jail it
left (#121).

Nothing to act on when upgrading: no new setting, and nothing in `pf.conf` or `rc.conf`.

### Added

- **A console password for cloud-image guests** (#159, closing #152): **Create a VM** has a
  **Console password** field.
  - With one, Jail Deck writes the guest's cloud-init user-data itself: vm-bhyve's own lines, the
    keys, and a bcrypt hash of the password for the image's default user, with
    `ssh_pwauth: false`. **The password opens the console, not SSH.**
  - It is never stored, logged or answered; the audit records `vm create … -u <file>`. Without a
    password nothing changes.
- **A Networks page** (#162, closing #128), read-only, between VMs and Operations.
  - Each bridge and vm-bhyve switch shows its kind (the default network, a tenant network, or the
    operator's own), its addresses, and whether Jail Deck's NAT rules translate it.
  - What is on it: a jail's epair links to the jail, a guest's tap to the guest. A switch whose
    bridge is not made yet says vm-bhyve makes it at `vm init`.
- **Which Jail Deck is running** (#160, closing #136): the version under the logo on every page
  (a release links to its notes), and a Jail Deck block at the end of the Host page with the
  commit, when it started, and the state directory.

### Fixed

- **Retaining a child dataset with its own mountpoint** (#161, closing #121). A child whose
  `mountpoint` was set on itself (a database at `/var/db/postgres`) kept it through the move,
  stayed mounted inside the jail it left, and made that jail's `zfs destroy -r` fail "busy".
  Retain now records the path in `jaildeck:mountpoint`, relative to the jail, and inherits;
  restore puts it back inside the new jail, so a jail of another name gets it at the same place.
  A dataset that was not mounted is left not mounted. A child that inherits its mountpoint moves
  as before.

### API

- **`console_password`** on `POST` and `PUT /api/vms` (#159): 8 to 72 bytes, no control
  characters, refused without being repeated. `PUT` checks it against the guest's seed, and a
  difference says only `set`, `none` or `another`. Additive.
- **`GET /api/about`** (#160): `version`, `commit`, `modified`, `started`, `state_dir`.
- **`GET /api/topology`** (#162): every bridge and switch, and what is on it. Not under
  `/api/networks`, where it would hide a tenant network named `topology`.
- **Retain and restore answer `409`** (#161), before anything runs, for a child whose own
  mountpoint is outside its jail, and for a recorded path that climbs out of the new one.
- **`jaildeck_build_info{version,commit} 1`** on the Prometheus endpoint (#160).

### Internal

- **The measurements are kept as captures:** the console-password seed as `vm_pw_user_data`,
  `capture-retain-mountpoint-induced.sh` with its `ret_*` outputs, and
  `capture-topology-induced.sh` with `top_ifconfig` and `top_switch_list`.
- **JD-038 and JD-025 amended** with what was measured: vm-bhyve takes `-u` or `-k`, never both;
  `zfs set` and `zfs inherit mountpoint` mount the dataset on their own.

### Known

- **A retained child's own children with local mountpoints** are moved as before, not handled
  (#121).

---

## v0.19.0 — 2026-10-29

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.19.0)

**Tenant networks** (#118 step 2, JD-043): a network of its own for each tenant, a bridge on its
own `/24` with a vm-bhyve switch on it, kept apart from every other network by PF. New jails and
guests also come with a **name suggested** (#148), and a guest's console no longer shows garbage
when something else on the host has it open (#153).

Nothing to act on when upgrading. One setting is new and optional: `JAILDECK_NETWORK_RANGE`
(default `10.92.0.0/16`), the range tenant networks take their `/24`s from. `pf.conf` needs
nothing beyond JD-040's three lines: tenant networks' rules share the default network's anchor
file, which stays byte-for-byte the same while there are none.

### Added

- **Tenant networks** (#151): created and destroyed through the API, in audited steps.
  - Each is a bridge `jdn<i>` on the lowest free `/24` of the range, the host's `.1` on it as the
    gateway, and a vm-bhyve **manual** switch named after the network.
  - Each gets NAT, a pass to its own gateway, and a block of every other VM network and the host.
    The default network reaches no tenant network.
  - **A step that fails takes back the earlier ones:** a network without its rules is one others
    can reach.
  - A guest joins one with its switch, a VNET jail with its bridge. Addresses are still the
    client's for now.
- **Name suggestions** (#150): **Create a jail** and **Create a VM** open with a name already
  given, an adjective and an animal (`jolly-otter`), and an **Another** button. A name in use is
  never suggested, and typing your own always wins.

### Changed

- **A guest's console is refused while another reader has its device open** (#154, fixing
  #153). Two readers on one serial line each get fragments of what the guest prints. v0.18.0 let
  a `vm console` on the host attach alongside; now the browser console closes at once and names
  the process holding the device (`cu (pid 38812)`), with how to close it. A reader that turns up
  during a session ends it the same way.

### API

- **`POST /api/networks`**, **`GET /api/networks`**, **`GET /api/networks/{name}`** and
  **`DELETE /api/networks/{name}?confirm=`** (#151). A delete is refused, naming them, while a
  guest's configuration names the switch, a jail's names the bridge, or anything is on it.
- **`GET /api/names/suggest`** (#150). After 20 taken draws it adds a two-digit number; after 40
  it answers `503`. `POST` and `PUT` still need a name.
- **A console's `closed` message gains `detail`,** and its reasons gain `console-shared` (#154).
  Additive. A guest's console with another reader on its device is accepted and then closed, not
  refused: a browser can't read a refused WebSocket handshake.

### Internal

- **`internal/names`,** Jail Deck's own package for the word lists: 142 adjectives and 125
  animals, every pair a valid jail, guest and host name.
- **The measurements are kept as captures:** `capture-tenant-network-induced.sh`, and the
  incident's own `fstat` output as `con_fstat_shared`.

### Known

- **A guest made from a cloud image can't be logged into at its console:** it has no password
  (#152).

---

## v0.18.1 — 2026-10-28

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.18.1)

An exec's audit entry says what its command did (#141). Nothing to act on when upgrading.

### Fixed

- **Exec's audit entry recorded `exit_code` 0 and no `timed_out`, whatever the command did**
  (#146). It was audited through the generic step logger, which records only Jail Deck's own
  failure to run a command. Found by the Otter Cloud contract test.

### API

- **An exec's audit entry carries the command's `exit_code`, `timed_out` and `duration_ms`**
  (#146). `success` still means Jail Deck ran the command: a non-zero exit is a successful
  exec (JD-016). A command that couldn't run is audited as before. A client reading exec
  entries from `GET /api/audit` now sees the real exit code where it saw `0`.

---

## v0.18.0 — 2026-10-28

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.18.0)

**A console in the browser** (#123, JD-042): a root shell in a running jail, and a running
guest's serial console, each from its own page, over a WebSocket. Sessions are audited with who
opened them, for how long and why they ended; **nothing typed or shown is recorded**.

Nothing to act on when upgrading. Two settings are new and optional: `JAILDECK_CONSOLE_IDLE`
(default `30m`) and `JAILDECK_CONSOLE_ORIGINS`, for a reverse proxy that rewrites `Host`. Behind
a proxy, it has to pass WebSocket upgrades through for the console to connect.

### Added

- **A jail's shell** (#142): **Open a shell** on a running jail's page puts a terminal
  (xterm.js) on the page, attached to `jexec -l <jail> /bin/sh -l` on a PTY.
  - Only `TERM` reaches the jail from Jail Deck's environment.
  - Stopping, restarting or destroying the jail closes its consoles first. An open shell
    used to make a stop take 10 s, measured.
- **A guest's serial console** (#143): **Open the console** on a running guest's page,
  attached to its `com1`.
  - One console per guest. `vm console` on the host can still attach alongside, and the
    page says so.
  - Closing it logs no one out of the guest.
- **What ends a session:** the page closing, the shell exiting, the jail or guest stopping,
  the login that opened it expiring or being revoked, or nothing typed for
  `JAILDECK_CONSOLE_IDLE`. The page says which, in words.

### API

- **`GET /api/jails/{name}/console` and `GET /api/vms/{name}/console`** (#142, #143). They
  upgrade to a WebSocket:
  - **Binary frames** carry the terminal's bytes.
  - **Text frames** carry JSON control: `resize` from the client, and `closed` with a reason
    from the server.

  The `Origin` is checked before anything opens. A stopped jail or guest gets `409`, and so
  does a second console on a guest.
- **Audit entries gain `principal`, `duration_ms` and `reason`** (#142). They appear only on
  `console-open` and `console-close` for now. Additive.

### Internal

- **Two Go libraries, the first in a while:** `github.com/coder/websocket` and
  `github.com/creack/pty`. JD-042 records the change of rule: small, focused libraries yes,
  frameworks no.
- **xterm.js is a devDependency,** bundled like everything else, and loaded as its own chunk only
  when a console opens.
- **The measurements are kept as captures:** `capture-jail-console-induced.sh` and
  `capture-vm-console-induced.sh`, with the probes they drive.

### Known

- **Exec's audit entry records `exit_code` 0 and no `timed_out`, whatever the command did**
  (#141). Fixed in v0.18.1.

---

## v0.17.1 — 2026-10-28

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.17.1)

The Dashboard and Jails pages load faster (#135). Measured on a host with 1,514 snapshots,
`GET /api/jails` went from 2.6 s to 0.06 s, and `GET /api/metrics` from 1.5 s to 0.69 s.

Nothing to act on when upgrading.

### Fixed

- **Listing jails walked every snapshot** (#138). The recursive `zfs get` that reads the jail
  datasets' tags visited each snapshot under them, though none has a local property to report,
  so it got slower with every snapshot taken. It now reads filesystems and volumes only, with
  the same output: 0.03 s instead of 2.52 s for that one call.
- **Disk health read one disk after another** (#138). `smartctl` took up to 0.57 s on a slow
  disk. Up to four disks are now read at once, and the results keep the scan's order, so
  nothing in the report changes.

---

## v0.17.0 — 2026-10-28

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.17.0)

Every node can have **a default VM network**, put in place by one explicit step: a switch
Jail Deck owns, with private ports and the host as gateway, IP forwarding, and outbound NAT
through a PF anchor whose rules Jail Deck writes (#118 step 1, JD-040). A guest's vCPUs can
be **pinned to host CPUs**, and `PUT /api/vms/{name}` **creates a guest idempotently** or says
how it differs (JD-041).

Nothing to act on when upgrading. Nothing on the host changes until someone runs
`jaildeck init vm-network` or presses **Set up** on the VMs page. **PF stays yours:** for
guests to reach the outside, add the three lines the panel shows to `pf.conf` once and reload
PF. Enabling PF over SSH can drop that session; a new connection works at once.

### Added

- **A Default network panel on the VMs page** (#130). Every part shows its owner: Jail Deck
  or you. Jail Deck's parts are put in place by **Set up**, and yours come with the exact
  commands and a Copy button. The create form defaults to the network: no DHCP is offered,
  the gateway is filled in, and the address hint is in its subnet.
- **`jaildeck init vm-network`** (#130), the same setup from the shell.
- **Pin to CPUs** in the create form, and the pin on a guest's page (#131).
- Settings: `JAILDECK_VM_SWITCH` (default `jaildeck`), `JAILDECK_VM_ADDRESS`
  (`10.88.0.1/24`), `JAILDECK_VM_EGRESS` (the default route's interface) and
  `JAILDECK_PF_ANCHOR_DIR` (`/usr/local/etc/jaildeck/pf`).

### API

- **`GET` and `PUT /api/networks/default`** (#130, JD-040). `GET` reports the network part by
  part, with an owner, a status and a remedy for each. `PUT` ensures Jail Deck's parts,
  audited as `create-vm-switch`, `set-vm-switch-address`, `set-vm-switch-private`,
  `enable-ip-forwarding`, `enable-ip-forwarding-at-boot`, `write-pf-anchor` and
  `load-pf-anchor`. A second call changes nothing. A switch of that name that is set up
  differently is refused with `409`, not overwritten.
- **`cpuset` on `POST /api/vms`** (#131, JD-041): one host CPU per vCPU, each a different
  one, checked against the host's CPUs before anything runs. It's written as
  `bhyve_options="-p 0:<cpu> ..."`, audited as `vm-pin-cpus`. `GET /api/vms/{name}` carries
  it. Additive.
- **`PUT /api/vms/{name}`** (#131, JD-041), with `POST`'s body. It answers:
  - **`201`** when it created the guest;
  - **`200`** when the guest already matches;
  - **`409` with `differences`** when it doesn't, leaving the guest as it is.

  It compares against what the host holds: the guest's configuration, its cloud-init seed
  and its disk's origin. `start` isn't compared.

### Fixed

- **A guest locked by this host was refused as if another host held it** (#122). For about a
  second while a guest powers itself off, vm-bhyve lists it locked by its own host. Start,
  stop, restart and destroy now wait that out.
- **The leftover sweeps changed the host without an audit entry** (#122). They now write
  `remove-leftover-epair` or `remove-leftover-rctl` when they removed something or failed to.

### Internal

- **The README's endpoint table had fallen 13 endpoints behind** (#122). It's complete again,
  and a test now fails for any route it lacks.

---

## v0.16.1 — 2026-10-27

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.16.1)

Releases are now built in CI and published into a signed `pkg` repository, so a host can
install and upgrade Jail Deck with `pkg` alone (#113, JD-039). Nothing in the application
changed: this is the first release to go out that way.

Nothing to act on when upgrading. A host can keep installing with `pkg add -f` as before, or
switch to the repository: install its public key and a `repos/jaildeck.conf`, then
`pkg upgrade -r jaildeck` (`docs/PACKAGING.md`). Either way the service is stopped and
started explicitly; the package still has no lifecycle scripts.

### Added

- **The package is built and attested in CI** (#114). Publishing a release runs
  `make package` from its tag in a FreeBSD 15.1 VM, and attaches the `.pkg`, its `.sha256`
  and a build provenance attestation to the release.
- **`packaging/freebsd/publish-repo.sh`** (#114), the publishing side: run periodically
  on the repository host, it accepts a release's package only if its checksum matches and its
  attestation says it was built in this repository, by `package.yml`, from that release's own
  tag; then it signs the repository with `pkg repo` and swaps it in. No GitHub token is
  needed, and the signing key never leaves the host.

---

## v0.16.0 — 2026-10-26

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.16.0)

Jail Deck creates virtual machines. A cloud image is prepared once as an image template, and
a guest is made from it with a NIC on the operator's switch, SSH keys and a static address
through cloud-init, then reached, stopped and destroyed — from the VMs pages or the API
(JD-037, JD-038). **This completes the Virtual Machines milestone.**

Nothing to act on when upgrading. Every endpoint from v0.15.0 answers as it did, and
`GET /api/vms` gained one field. Two things appear on the host the first time they are used,
both under vm-bhyve's datastore: the `.images` dataset, the first time an image is prepared,
and Jail Deck's own template, `.templates/jaildeck.conf`, the first time a guest is created.

### Added

- **Images on the VMs page** (#108): prepare an image from a source, watch it while it is
  fetched and written, see which guests use it, remove it. The panel says how a preparation
  it started came out — ready, or stopped short with a pointer to Operations.
- **Create a VM and Destroy** (#110). The form offers ready images and vm-bhyve's switches,
  DHCP or a static address, and SSH keys, and says what each choice means: no switch is no
  network, and no key is console only. Destroy is on a stopped guest's page and asks for the
  name typed.

### API

- **`GET`, `PUT` and `DELETE /api/vm-images/{name}`, and `GET /api/vm-images`** (#107,
  JD-037). `PUT` answers `202` and the client watches the image (JD-008): the source's
  archive is fetched and checked against the publisher's `SHA512SUMS`, its disk written by
  `tar -xO` straight into a sparse zvol, and sealed with `@base`. One source to start with,
  `debian-13`. Removal is refused while a guest's disk is cloned from the image. Audited as
  `fetch-image`, `write-image`, `seal-image` and `destroy-image`.
- **`POST /api/vms` and `DELETE /api/vms/{name}`** (#109, JD-038). Create runs `vm create`
  from Jail Deck's UEFI template with a cloud-init seed, adds a NIC on the named switch —
  checked to exist, because vm-bhyve does not — and replaces the guest's disk with a clone
  of the image. Everything is checked first, and a step that fails destroys the guest again.
  Destroy is for stopped guests only. Audited as `vm-create`, `vm-add-network`,
  `vm-replace-disk` and `vm-destroy`.
- **`GET /api/vms` carries `switches`** (#110), vm-bhyve's switches by name, read when
  vm-bhyve is ready. Additive.

### Internal

- **Every image and every guest disk cloned from one is `volmode=dev`** (#107). Measured: a
  clone grown past its image in GEOM's default mode was held open by the kernel's GPT
  auto-resize, and the guest on it booted nothing.
- **The command runner can send a command's output into a file** (`StdoutFile`, #107), which
  is how tar writes an image's disk into its zvol with no shell and no temporary file.

---

## v0.15.0 — 2026-10-26

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.15.0)

Jail Deck can start, stop and restart vm-bhyve's guests, from the VMs pages or the API
(JD-036). These are the first VM operations that change the host.

Nothing to act on when upgrading. Every endpoint from v0.14.0 answers as it did; there are
three new ones.

### Added

- **Start, Stop and Restart on the VMs pages** (#104). A running guest offers Stop and
  Restart, a stopped one Start, a suspended one Resume. One that is booting or held by
  another host offers nothing and says why. Stop and Restart ask first, and the question
  is the deadline: a minute for the power button, then a poweroff that loses whatever the
  guest had not written.

### API

- **`POST /api/vms/{name}/start`, `/stop` and `/restart`** (#103, JD-036). Each step is
  confirmed in `vm list`, because neither `vm start` nor `vm stop` waits for the guest.
  A stop that the guest does not answer within 60 seconds becomes `vm poweroff -f`, and
  the answer says `forced: true`. Starting a running guest or stopping a stopped one runs
  nothing. A guest locked by another host is a `409`, and so is stopping one in its boot
  loader, which vm-bhyve would silently decline. Audited as `vm-start`, `vm-stop` and
  `vm-poweroff`.

### Internal

- **The command runner can detach a command** (#103). `vm start` leaves vm-bhyve's
  supervisor in its caller's process group, measured, and the runner kills a whole group
  on a timeout — which would have powered the guest off. A detached command gets a session
  of its own, and a timeout signals it alone. Measured live: stopping Jail Deck left a
  started guest running.

---

## v0.14.0 — 2026-10-26

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.14.0)

Jail Deck can now see virtual machines. vm-bhyve's guests are listed with their state, in
the API and on a new **VMs** page, read-only (JD-035). Nothing in this release can start,
stop or change a guest.

Nothing to act on when upgrading. Every endpoint from v0.13.0 answers as it did; there are
two new ones.

### Added

- **A VMs page** at `/vms`, with a page per guest (#100). It starts with vm-bhyve's own
  state — not installed, not configured (in vm-bhyve's words), or ready — then lists each
  guest's state, CPU, memory and whether it starts at boot. A guest's page shows its
  configuration, its console, its network interfaces and its disks.

### Fixed

- **At phone width every page scrolled sideways** (#100). The navigation becomes a row
  across the top there, and the rail kept the wide layout's `align-self: flex-start`, so it
  took the width of its links rather than the screen's. The row now scrolls on its own.

### API

- **`GET /api/vms`** lists every guest vm-bhyve knows, from `vm list`, read on every
  request (#99, JD-035). `vm_bhyve` is `ready`, `not_installed` or `not_configured`, and
  all three are a `200`; not configured carries vm-bhyve's own words in `problem`. A
  guest's `state` is one of vm-bhyve's five — `running` and `bootloader` with a `pid`,
  `stopped`, `suspended`, and `locked` with the host in `locked_by`. `cpu` and `memory`
  are the configuration as written (`"2"`, `"512M"`), not re-parsed.
- **`GET /api/vms/{name}`** adds what `vm info` says: UUID, resident memory, consoles,
  network interfaces and disks, in bytes. A name the listing does not have is a `404`, one
  vm-bhyve could never have created a `400`, and a vm-bhyve that cannot list a `409`.

---

## v0.13.0 — 2026-10-26

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.13.0)

Disk health finally works in the packaged service, and the **Virtual Machines** milestone
begins: JD-019 is locked on vm-bhyve, and Host Setup reports whether a host is ready for
it.

**One thing to act on when upgrading: `rc.d/jaildeck` changed.** A `pkg add -f` of the
package replaces it, as `docs/PACKAGING.md` describes; an install made with `make install`
needs it installed again. Without the new script the service still cannot find `smartctl`
or `vm`. Every endpoint from v0.12.0 answers as it did; the metrics report and the
readiness report only gained fields and checks.

### Added

- **Host Setup reports what VMs need beyond the CPU** (JD-019). Two checks join
  `cpu_virtualization` under the `vms` capability. `vm_bhyve` asks vm-bhyve itself — `vm
  list`, read-only — and reports its own words for whatever is wrong (not installed,
  `vm_enable` unset, no `vm_dir`, a datastore that is missing or not mounted), with the
  remedy. `uefi_firmware` checks for edk2-bhyve's `BHYVE_UEFI.fd`. `vm init` is not
  required: `rc.d/vm` runs it at boot, and `vm list` answers the same either way.

### Fixed

- **Disk health never worked in the packaged service** (#95). rc starts services with
  `PATH=/sbin:/bin:/usr/sbin:/usr/bin`, and `smartctl` lives in `/usr/local/sbin`, so a
  host with smartmontools installed reported no disks — the answer it gives for a host
  without it. Every test passed because each one started the binary by hand. The rc
  script now adds `/usr/local/sbin` and `/usr/local/bin` to `PATH`, before the env file
  so an operator can still set it there. It matters again for `vm-bhyve`, which installs
  `vm` in the same place.

### API

- **`disks_unavailable` says why `disks` is empty** when it is not because the host has
  none (JD-032 promised this and the field did not exist). It names a `smartctl` that is
  not on Jail Deck's `PATH` together with the `PATH` it searched, and passes on the scan's
  own complaint — an unprivileged `smartctl --scan` prints
  `# scan_smart_devices: Permission denied` and exits 0. The Dashboard shows the reason
  instead of guessing one: it had been telling a host with smartmontools installed to
  install it.

---

## v0.12.0 — 2026-10-25

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.12.0)

Closes the **Firewall Visibility** milestone, and gives the interface tests. Nothing to act
on when upgrading: every endpoint from v0.11.0 answers as it did, and `rc.d/jaildeck`,
`.env.sample` and the package's contents are unchanged apart from the binary.

### API

- **Firewall visibility, read-only** (#16, JD-034). `GET /api/firewall` answers PF's state,
  counters, the rules with per-rule counters, NAT, every anchor's rules and the tables;
  `GET /api/firewall/tables/{name}` one table's addresses; `GET /api/firewall/log` the most
  recent pflog lines. Nothing reachable from the API can change PF (JD-020).

  **`state` is `not_loaded`, `disabled` or `enabled`, and none is an error.** Not loaded is
  a `200` with nothing else in it — the reference hosts do not load PF. Disabled with rules
  is a policy loaded and not applied, and the rules read exactly as enforced ones would, so
  `state` is the field to read first.

  `nat` is separate because `pfctl -s rules` does not show NAT at all, and each anchor's
  rules are listed under `anchors` because the main listing does not include them. A rule's
  `text` is pfctl's own rendering, not re-parsed. **Tables carry no address count**:
  pfctl's own is wrong on 15.1, so the count is the table's contents.

  **`for_seconds` is pfctl's clock, which restarts at every enable and disable and counts
  from boot otherwise** — a PF loaded and never enabled reports the host's uptime as its
  time disabled.

  pflog lines may lag by pflogd's flush delay (60 s by default), which Jail Deck does not
  trigger; `log` records the packet that creates a state, not every packet; and an anchor
  is named in the log by its last component only. A log too large to read to its end comes
  back with `truncated: true` and **no lines**, rather than its oldest lines.

### Added

- **A Firewall page** leading with PF's state — not loaded, disabled (an amber banner:
  none of these rules is being applied), or enabled — then the rules, NAT, anchors, tables
  opened on demand, and the recent log.

### Development

- **The frontend has tests** (#77). Vitest with `@testing-library/svelte` in jsdom renders
  the real components and asserts what they send; `make web-check` runs them with the type
  check, which on its own had passed every bug v0.9.0's interface shipped. The create
  form's `buildSpec` is a module of its own, and the tests fail when each v0.9.0 bug is
  put back. A frontend change is still not verified until someone has looked at it
  (`docs/UI.md`).

---

## v0.11.0 — 2026-10-25

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.11.0)

Finishes the **Metrics** milestone and delivers the first half of **Packaging**. Nothing
to act on when upgrading: no setting, credential or file changes shape.

### API

- **Disk health in the metrics report.** `GET /api/metrics` gained `disks`: every
  physical device `smartctl` can read, pooled or not, with the drive's own verdict, the
  attributes smartctl flags, the most recent self-test, and the pool and vdev it holds up
  (#83, JD-032).

  **There is no health score.** `passed` is the drive's own overall assessment and
  `failing` / `failed` are what smartctl says is below threshold now and ever — and they
  routinely disagree. The disk that prompted this reports `passed: true` with attribute
  184 below threshold. Both are reported; neither is summarised into the other.

  An attribute's `raw` is the **string** smartctl printed, never a number: vendors pack
  several fields into it. An NVMe carries an `nvme` block instead of an attribute table,
  because it has none. Where smartctl must be given a different path than
  `/dev/<device>` — an NVMe is `nda0` to ZFS and the kernel but readable only as
  `/dev/nvme0` — `smart_path` says so. Self-tests are read, never started.

  `disks` is an empty list, not an error, on a host without `smartmontools`, which is a
  package rather than part of base. A disk that cannot be read never fails the report.

- **`cpu.ticks_per_second`** says what a `cpu.ticks` counter is worth: `kern.clockrate`'s
  `stathz`, which is not its `hz`. On the reference host they are 127 and 1000, so a client
  converting ticks with `hz` reported CPU time eight times too small (JD-033).

- **`GET /metrics`** serves the same report in the Prometheus text exposition format, at
  the bare path a scraper expects (#15, JD-033). **It requires the same token as the rest
  of the API**, over TCP and the Unix socket alike — on FreeBSD a jail with `ip4=inherit`
  shares the host's loopback, so binding `127.0.0.1` is not a boundary. Scrape it with
  `authorization.credentials_file` and a token from `jaildeck token add`.

  Names follow Prometheus conventions — bytes, seconds, ratios — rather than the JSON
  field names. A jail's CPU is `jaildeck_jail_cpu_cores`, not `pcpu`. Verified against
  Prometheus 3.15.0: `promtool check metrics` passes, and both a TCP and a socket scrape
  come up healthy. Not part of the Swagger spec, which describes the JSON API.

  Until now `/metrics` fell through to the web interface and answered `200` with an HTML
  page; it now answers `401` without a token.

### Added

- **A Disks section on the Dashboard**, worst first: the pool each disk holds up, model,
  serial, size, temperature, age and a badge that takes the worse of the drive's verdict
  and its flagged attributes. A flagged disk says why, directly under its row.

- **Native FreeBSD packages.** `make package VERSION=X.Y.Z` builds and verifies
  `bin/packages/jaildeck-X.Y.Z.pkg` on FreeBSD 15 amd64 — binary, rc.d script and sample
  configuration, nothing else — refusing any build whose inventory, modes or embedded
  frontend do not match what was declared (#84). Installing it creates no credential,
  dataset or configuration and starts nothing. See `docs/PACKAGING.md`.

  A packaged binary reports `jaildeck version` as `vX.Y.Z`, the same shape as one built
  from a tag; the package name and manifest carry the bare `X.Y.Z` that `pkg` expects.

### Fixed

- **`make -j all` could ship a binary without the web interface**, exiting `0`: the Go
  build started while Vite had emptied `web/dist`. The two now run in order.
- **`make help` hung under FreeBSD's base make**, which has no `MAKEFILE_LIST`.

---

## v0.10.0 — 2026-10-24

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.10.0)

### API

- **Run a command inside a jail.** `POST /api/jails/{name}/exec` takes
  `{"argv": [...], "timeout": "60s", "env": {...}}` and returns the command's stdout,
  stderr and exit code — JD-007's "hand off to in-jail provisioning" made callable
  (#12, JD-016). It is root in the jail, which is no more than this API already implies.

  **A non-zero exit is still a `200`.** The exec succeeded; the thing it ran failed, and
  `exit_code` is the answer. A client reading the HTTP status instead is reading the wrong
  field. `exit_code` is `-1` when the command never ran or was stopped by the timeout.

  **The environment is clean.** The command runs under `jexec -l`. Measured on
  15.1-RELEASE-p5: plain `jexec` hands the jail the caller's entire environment, so
  everything the Jail Deck process was started with — `jaildeck.env` included — would be
  readable by anything run through this endpoint. `-l` replaces it with `HOME`, `PATH`,
  `SHELL`, `USER` and `LANG`, and `env` puts the caller's own variables on top.

  `argv` is an array and there is no shell: nothing splits a string, so a quote or a
  semicolon in an argument is just a character. A caller who wants a shell asks for one.
  There is no stdin.

  **`timeout`** is 60s by default and five minutes at most (JD-014); longer is refused
  rather than clamped, because a command silently given less time than asked for looks
  like one that failed. A timed-out command is killed **inside the jail**, along with
  anything it forked, and whatever it printed first is still returned.

  Each stream is capped at 1 MiB and says so with `stdout_truncated` / `stderr_truncated`.
  Every call is audited with the command line it became.

- **`system.Command` gained `MaxOutput`**, honoured by the command runner as a bounded
  writer rather than a truncation afterwards — a command that prints a gigabyte would
  otherwise be held in memory in full before anyone decided it was too much. Zero, which
  every command Jail Deck composes itself uses, keeps everything as before.

### Added

- **Host and per-jail resource usage.** `GET /api/metrics` answers the host — load
  average, CPU, memory and pools — and every running jail's CPU, memory and process count,
  in one request. A **Dashboard** page renders it, and `/` now lands there (JD-031).

  Per-jail usage comes from `rctl -u` where `kern.racct.enable` is on and from summing
  `ps -J <name>` where it is not, and the response says which. Measured on three jails: the
  `ps` resident sums land within 1% of rctl's `memoryuse` and the process counts are
  identical, because racct's `memoryuse` **is** the sum of the same per-process resident
  sizes.

  **`cpu.percent` is absent on the first call after a restart.** `kern.cp_time` is
  cumulative and there is no kernel-maintained utilisation figure, so a percentage exists
  only between two readings; `cpu.over_seconds` says how long the interval behind it was.
  The raw counters are always there. Nothing is stored — a chart over time is what a
  Prometheus endpoint will be for.

---

## v0.9.0 — 2026-10-24

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.9.0)

### API

- **Create a jail from a ready template.** `POST /api/jails` takes the whole spec — template,
  hostname, network, limits, jail parameters, child datasets, tags, autostart — and runs the
  sequence `docs/SPEC.md` describes: clone the template's `@base` into
  `<pool>/jails/containers/<name>` **in the template's own pool**, make the child datasets and
  set the tags, write `/etc/jail.conf.d/<name>.conf`, add it to `jail_list` if asked, start it.
  (#8, JD-007, JD-030)

  **The start is confirmed, not assumed.** `service jail start` exits 0 whether or not the jail
  came up, so every start is followed by `jls -j <name>`. Measured on 15.1-RELEASE-p5, along
  with the rest of the ordering.

  **A step that fails stops the create and nothing is undone.** The response says which step and
  what exists so far — a dataset that was cloned stays cloned, a stanza that was written stays
  written — because an automatic rollback is a second destructive operation running at the moment
  least is known about the state. The one exception is a leaked epair, below.

- **Destroy a jail.** `DELETE /api/jails/{name}?confirm=<name>` stops it, takes it out of
  `jail_list`, deletes its file in `jail.conf.d`, and destroys its dataset, in that order.
  **Its child datasets go with it** and are named in `taken`; anything worth keeping is moved out
  with `retain` first. Snapshots, clones and holds refuse the whole thing with each one named and
  nothing touched — `zfs destroy -R` is never passed, which is what would make that check
  decorative, and `-r` only when the jail has children, so a snapshot taken in the gap makes ZFS
  refuse rather than be swept up silently. (#11, JD-030)

  A jail with no file of its own in `jail.conf.d` is refused: its block is in `/etc/jail.conf`,
  which belongs to the operator and which Jail Deck never writes, so destroying the dataset would
  leave the jail declared and pointing nowhere.

- **VNET jails on an operator's bridge.** `"network": {"mode": "vnet", "bridge": …, "private": …,
  "ip4": …, "gateway": …}`. The epair is created in the stanza's `exec.prestart` and destroyed in
  its `exec.poststop`, so a jail started from a shell gets the same network. The bridge is the
  operator's: Jail Deck adds and removes its own epairs and never creates one. (#9, JD-017)

  Four things measured on 15.1-RELEASE-p5 shaped it. `ifconfig epair create` prints only the `a`
  end, so the unit is chosen when the stanza is written — from what no interface uses *and* no
  jail configuration claims. `ifconfig <host end> destroy` takes the jail end and the bridge
  membership with it, so cleanup is one command. A VNET jail's `lo0` starts **down**, which breaks
  anything talking to 127.0.0.1, so the stanza brings it up before `/etc/rc`. And **`private`
  isolates a jail from the other private members only** — a private member still reaches
  non-private ones and the bridge itself, which is narrower than it sounds.

  **A start that fails after `exec.prestart` leaves the epair behind** and `jail(8)` never runs
  `exec.poststop` for it, so the orphan would block every retry at `already exists`. Jail Deck
  destroys it itself and says so; a cleanup that fails names the interface rather than going quiet.

  **A VNET jail reports no `ip` in the listing.** It sets no `ip4.addr`, so neither `jls` nor its
  configuration carries one, and an unknown assignment in a stanza is a hard error — so there is
  nowhere honest to record it either. The create response carries the address that was asked for.

- **Resource limits and the jail-parameter allowlist.** `"limits"` is rctl rules plus a cpuset;
  `"params"` is jail(8) parameters, and only the nine on the allowlist — `devfs_ruleset`,
  `securelevel`, `enforce_statfs`, `children.max` (`0` only), `sysvmsg`, `sysvsem`, `sysvshm`,
  `allow.chflags`, `allow.set_hostname`. Anything else is refused with the parameter named,
  because a denylist fails open the day jail(8) gains a parameter. (#10, JD-018)

  Limits are applied by the stanza's `exec.poststart` and removed by its `exec.prestop`. Measured:
  **jail(8) destroys a jail whose `exec.poststart` fails**, so a jail whose limits could not be
  applied never runs — "refusing beats running unlimited" enforced by jail(8) itself, on every
  start. Also measured: `pcpu` takes `deny`, not `throttle`; and `rctl -l jail:<name>` does not
  work at all — the listing form for a jail is the bare filter `rctl jail:<name>`.

  **rctl rules are keyed to the name and outlive the jail.** A jail removed without `rctl -r`
  leaves its rules in the kernel and the next jail of that name inherits them, so a create refuses
  a name that still carries rules — naming them and the one command that clears them — and a
  destroy sweeps whatever is left. A start that fails part way through `exec.poststart` has the
  rules it had already applied taken back.

  **A `devfs_ruleset` the kernel has not loaded is refused.** Measured: an unloaded number names an
  empty ruleset, an empty ruleset hides nothing, and the jail comes up with the host's **whole
  `/dev`** — `ada0`, `mem`, `kmem`, `io`, `bpf` — while `jls` reports the number that was asked
  for. A stanza with no `devfs_ruleset` at all gets the restricted set.

- **Idempotent create.** `PUT /api/jails/{name}` with the same spec: absent → created, `201`;
  already what the spec describes → `200` with `changed: false`; there but different → `409` with
  the differences. **Nothing is ever reconciled** — a jail that differs is left exactly as it is
  and the client decides. Safe to repeat after a timeout. (#8, JD-016)

  "Matching" is decided by rendering the stanza the spec would write — against the path the host
  reports and the epair the existing stanza already claims — and holding it against the stanza on
  disk, setting by setting. So the comparison covers every field the generator writes rather than
  a list that has to be kept up to date. Line order, comments, blank lines, the quoting of a value
  and `$name` are not differences; anything else is, including a setting only one side has.
  `template` comes from the root's clone origin and `autostart` from `jail_list`.

  **`start` is deliberately not compared.** It says whether to start the jail once it has been
  configured — an instruction about the create, not a property of the jail. Comparing it would make
  a jail stopped for maintenance refuse every `PUT`.

- **A jail spec carries its child datasets and its tags** (#10, JD-007). `datasets` is a
  list of datasets created one level beneath the jail's own — each with the native ZFS
  properties of JD-023's allowlist — and `properties` sets ZFS user properties on the
  jail's own dataset. Both are applied after the root is cloned and before the stanza is
  written, so a jail whose `/etc/rc` expects its data mounted finds it there and a quota
  is in force before the first write. The idempotent `PUT` compares both.

- **Breaking: a child dataset mounted outside its jail is refused**, on `POST /api/jails`,
  on `PUT /api/jails/{name}` and on `PUT /api/jails/{name}/datasets/{child}`, which took
  any mountpoint before. Measured on 15.1-RELEASE-p5: ZFS creates such a dataset without
  complaint and the jail cannot see it, yet `zfs destroy -r` on the jail destroys it
  anyway — so destroying the jail would take a dataset living elsewhere on the host.
  A dataset meant to outlive the jail goes through
  `POST /api/jails/{name}/datasets/{child}/retain` instead, which moves it out of the
  jail's dataset tree.

- **A release template is brought to its patch level and sealed.** The last two stages,
  which carry a prepare from `extracted` through `updating` to **`ready`** — a template a
  jail can be cloned from, prepared end to end from the interface with no shell steps.

  `freebsd-update -b <root> --currently-running <release> --not-running-from-cron fetch
  install`, every flag measured against the real tool on the deployment host. The **plain
  release** is what `--currently-running` wants, not a patch level: a freshly unpacked
  `base.txz` is exactly the release as published. `--not-running-from-cron` is what makes
  it non-interactive. `fetch install` in one invocation exits 0 even when there is nothing
  new, which is the ordinary case for a recently published release.

  **The `@base` snapshot is taken last, and that ordering is the point.** `base.txz` is
  the release as published, so a template sealed before `freebsd-update` would carry every
  advisory since into every jail cloned from it. An update that fails leaves the template
  `extracted` and unsealed — honestly not ready — with `freebsd-update`'s own sentence in
  the operations log.

  Nothing records which patch level it reached. `freebsd-update` says so in its own output,
  which the audit log keeps, and `freebsd-version -u` inside a jail cloned from the
  template is the live answer; a number stored here could disagree with the userland it
  describes (JD-011).

- **A fetched release is unpacked into a template dataset.** The second stage, which
  carries a prepare from `fetched` to `extracted`: create an empty dataset under
  `<pool>/jails/templates/`, `tar -xpf` the archive into it, and copy the host's
  `resolv.conf` and `localtime` in — without the first a jail cloned from this template
  cannot reach a package mirror, and without the second it logs in UTC while the host does
  not. The tar keeps permissions and flags; a userland extracted without them has a
  world-writable `/etc`.

  **Nothing is ever unpacked over an existing dataset.** A dataset holding half a userland
  looks exactly like one holding a whole one, so a template that already has a dataset and
  no `@base` is a `409` naming itself — the same refusal a derive already made, now the
  rule for every path (JD-027). A failed extract is left exactly where it stopped: the
  dataset is the evidence, and the audit log carries `tar`'s own sentence.

  Repeating a call **resumes**: an archive already in `media/` is not downloaded again.

- **A release template's archive can be fetched from the FreeBSD mirror.** The first stage
  of preparing one, and the first operation to use JD-008's answer:
  `PUT /api/templates/{name}` with `{"release": "15.1-RELEASE"}` **returns 202 and does not
  wait**. An empty release means the host's own, which is also the ceiling — a jail's
  userland must be the same as or older than the kernel it runs under.

  It fetches that release's `base.txz` into `<pool>/jails/media/{release}/` and **verifies
  it against the release's own MANIFEST** before reporting success. That is not politeness:
  an archive that arrived corrupt would be extracted into a template every later jail is
  cloned from.

  **The release, never a patch level.** `kern.osrelease` says `15.1-RELEASE-p5` and the
  mirror publishes no `-pN` directory at all, so the patch level is dropped — it is what
  `freebsd-update` brings the extracted userland to afterwards, not what is downloaded.

  **Templates now report `state`.** Read off the filesystem — the archive is in `media/` or
  it is not, the dataset exists or it does not, the `@base` snapshot exists or it does not
  — so a reload, or a restart of Jail Deck, shows where the work actually got to. A
  template that has only been fetched has no dataset and still appears, because the
  resource a client was told to watch has to exist from the moment the work starts.

  **`running`** is beside it when a step is executing, with the step and when it began. In
  memory only; a second request for a template already in flight is a 409 rather than a
  queued one.

  Naming both `from` and `release` is a 400: they are two ways to build a template, and
  picking one for the caller would be guessing.

- **A dataset reports the native ZFS properties it sets.** `GET /api/jails` and
  `GET /api/jails/{name}` carry a `properties` map beside each dataset: the four an
  existing dataset may be given (JD-023), as the strings `zfs` prints — so `refquota`
  reads `2G` and a client can send the same value back. (#55)

  **The value the dataset sets, not the value in effect.** A property inherited from the
  dataset above is absent. Showing it would be friendlier right up to the moment someone
  edited it: the write would create a local override on the child rather than change the
  setting they were looking at.

  Only the allowlisted four. `canmount` is locally set on real datasets and stays out for
  the same reason it is out of the write list — it would stop a jail's dataset mounting.
  `mountpoint` keeps its own column.

  It costs no extra command: the `zfs get -s local all` that reads the tags carried these
  all along and was discarding them. The jail's user properties stay at the jail level
  (JD-004); nothing moved.

### Added

- **Creating and destroying a jail from the interface.** `Create a jail` on the Jails page opens a
  form at `/jails/new`: Source, Network, and then Limits, Jail parameters and Datasets and tags,
  which open only when asked for, so a jail that needs none of them reads as one that needs none.
  The fields that exist under Limits and Parameters are exactly the ones the API accepts — the
  form is the allowlists made visible — and the network mode decides which fields exist at all,
  because a field set for a mode that ignores it is refused rather than dropped. (#8, #11)

  **The result is the create's own report**, rendered as the steps the server ran in the order it
  ran them — clone, datasets, configure, autostart, start — each done, failed or not reached. A
  create that stopped half way shows exactly how far it got, because nothing is undone and that
  report is the recovery information.

  **Destroy** is its own section at the foot of a jail's page, in the danger colour. The
  confirmation names the child datasets that will go with the jail and asks for the jail's name to
  be typed, which is the API's own requirement rather than invented ceremony. A refusal over
  snapshots, clones or holds stays on the page with each one named — they have to be dealt with one
  at a time, so a message that disappears is no use.

- **Preparing a release from the Templates page.** A form for the release, a `State`
  column in place of the ready badge, and the step with its elapsed time under it while
  something is running. The page polls only while anything is in flight and stops when
  nothing is; a template being prepared cannot be removed. What each stage actually ran is
  on the Operations page, which is where a failure is explained.

- **A jail's datasets show their properties, and a child's can be changed.** The chips
  under each dataset name are what it sets itself; a child gets a **Properties** form
  prefilled with them. An empty box leaves the property alone — there is no route that
  makes a dataset inherit one again. The jail's own dataset shows its properties and has
  no form, because the API writes to children and not to the dataset the jail runs from.

### Changed

- **JD-008 is decided: a long-running operation returns `202` and you watch the
  resource.** Preparing a release template will not hold the request open for the minutes
  it takes. There is **no job and no job id** — the thing being watched is the template,
  at the URL it keeps for the rest of its life.

  `state` is read off the filesystem, not recorded: `media/<release>/base.txz` exists, the
  dataset exists, the `@base` snapshot exists. JD-011 had already named those checks as
  the template's state machine. So a restart or a client that never comes back costs
  nothing — the next `GET` reports where the work actually got to, and repeating the call
  resumes from whatever stage is already a fact on disk.

  The one thing that cannot be derived — whether a step is executing *now* rather than
  having stopped half way — is held in memory and reported as `running`. Deliberately not
  persisted: a restart kills the work, so a marker that survived it would be claiming
  something false.

  Deriving a template with `pkg -c` **stays synchronous**. The line is not "slow" in the
  abstract but whether the operator needs to be told what is happening to keep using the
  tool: ~25 s measured on the deployment target is a spinner; a 372 MB fetch plus
  `freebsd-update` is not.

  No code yet — this is the decision and the records that pointed at it. The
  implementation is the rest of the Templates milestone.

## v0.8.0 — 2026-10-23

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.8.0)

### API

- **Templates.** `GET /api/templates` lists them with their readiness and lineage, both
  read from ZFS. Two ways to build one. (#7, JD-027)

  **`POST /api/jails/{name}/template`** clones a jail you configured — the FreeBSD
  Handbook's "Cloning a Jail as a Template" (17.4.3). It captures whatever is in the jail,
  not only packages, and is the general path. Snapshot the jail as `@base`, clone it into
  `templates/<name>`, then **`zfs promote`** the clone.

  The promote is the step that is easy to omit and expensive to omit: without it the
  template is a clone *of* the jail, and `zfs destroy` on that jail answers `filesystem has
  dependent clones`. After it the jail is a clone of the template, which is also the right
  way round — a template should outlive the jail it came from.

  The jail must be **stopped**, and this refuses rather than stopping it: a snapshot of a
  running jail is an image caught mid-stride, and taking a service down is the operator's
  call. A jail already carrying `@base` is refused too, since reusing it would capture
  whenever that snapshot was taken rather than the jail now.

  **`PUT /api/templates/{name}`** is the shortcut for the package-only case: clone a
  parent's `@base`, `pkg -c <root> install -y`, snapshot as `@base`.

  **`GET /api/templates/{name}`** is one template with its `dependents` — every clone and
  every hold — which is what a removal would refuse over, answerable before asking for one.
  Both lists are present and empty rather than absent.

  **`DELETE /api/templates/{name}?confirm={name}`** destroys one, and **refuses while
  anything is cloned from it**, naming every jail and derived template in the way. ZFS
  would refuse too, but only after being asked and without saying which; this asks
  `zfs get clones` first. Never `zfs destroy -R`, which would take them with it. It is also
  what cleans up the half-built template a failed derive leaves behind.

  `pkg -c` chroots, so nothing is started: no jail, no stanza, no networking for a
  temporary jail. It is the same shape as the `freebsd-update -b <root>` JD-007 already runs
  against an unbooted template.

  **It installs packages and nothing else.** Anything further belongs to the other path —
  clone a template, create a jail on it, exec into it, snapshot the dataset — which is more
  capable and is the general answer. The two are the same mechanism at different times:
  provision at jail creation, or provision once into a template and clone it.

  Nothing is stored. `ready` is whether `@base` exists; `parent` is read back from
  `zfs get origin`. A failed install leaves the clone without its `@base`, which the listing
  reports as not ready — no cleanup is needed for that state to be truthful — and a second
  attempt on it is refused rather than resumed.

  This also settles the **storage domain granularity** question `docs/SPEC.md` had open:
  templates get their own bounded context, `internal/templates`.

- **A jail's root can be re-cloned from a newer template, keeping its children.**
  `POST /api/jails/{name}/reclone?confirm={name}` stops the jail, moves every child dataset
  aside, parks the old root, clones the new one, moves the children back and starts it
  again — the upgrade path for a jail whose state lives in a child (#6, JD-007, JD-026).
  The jail ends in the state it was found in.

  **Nothing is destroyed.** The old root is renamed to
  `<pool>/jails/retained/<jail>-root-<snapshot>`, so a re-clone onto the wrong template
  costs one call to undo. It is a clone and occupies only what the jail wrote since it was
  made.

  **If a step fails, nothing is retried and nothing is undone.** The response names the step
  and lists every dataset that is no longer under the jail, with where it now sits, so it
  can be restored with the call JD-025 provides. An automatic undo that itself fails leaves
  two interleaved failures and a host in a state neither describes.

  Refusals happen **before** the jail is stopped: a mistyped template name costs an error
  rather than an outage. The template is the full `dataset@snapshot` with no default,
  because every jail is already a clone of `templates/<release>@base` and defaulting to it
  would be a no-op dressed as an upgrade.

### Changed

- **The documentation is published as a site.** `docs/` is now a Jekyll site built by
  GitHub Pages at [otter-labs.com/jaildeck](https://otter-labs.com/jaildeck/): the
  specification, architecture, decisions, roadmap, interface notes and the API guide, with
  the generated spec downloadable at `/jaildeck/api/swagger.json`.

  Nothing moved, so every existing link into `docs/` still works. Each page gained front
  matter and a link back to the index, because the theme has no navigation of its own.

  The site builds from `main`, and pull requests land on `dev` first, so the published
  version tracks releases rather than development.

- **The API is described by a generated OpenAPI spec, not a hand-maintained collection.**
  `collections/insomnium.json` is gone. `make doc` generates
  [`docs/api/swagger.json`](docs/api/swagger.json) from annotations on the handlers, using
  `swag` pinned in `go.mod` as a tool dependency — no global install, same version
  everywhere. Import it into any client that reads Swagger 2.0.

  The collection was 1,648 lines of JSON edited by hand for one tool. The guarantee it
  carried is kept and strengthened: `make check` now fails if the committed spec is not what
  the annotations produce, and the suite fails in both directions besides. That matters
  because `swag` silently skips an un-annotated handler, so an undocumented endpoint would
  otherwise produce a spec that is merely shorter.

  Jail Deck serves no Swagger UI; the spec is a file. The long-form API guide moved to
  [`docs/api/index.md`](docs/api/index.md).

  No endpoint changed. `common.ErrorResponse`, `auth.LoginRequest` and
  `storage.RetainedDataset` are named types now rather than inline literals, so the spec can
  point at them — the JSON each produces is identical.

### API

- **A dataset can be moved out of a jail and back in.**
  `POST /api/jails/{name}/datasets/{child}/retain` moves it to
  `<pool>/jails/retained/<name>`, `GET /api/retained` lists what is being kept, and
  `.../restore` brings one back under any jail. Destroying a jail takes its children and
  their snapshots with it (JD-007), so this is what makes "destroy the jail, keep the data"
  possible. (#5, JD-025)

  Three audited commands: `zfs rename -u`, `zfs unmount`, `zfs mount`. **The `-u` is not
  optional** — a plain `zfs rename` unmounts and remounts inside the same command, which on a
  running jail is the filesystem vanishing from under a live process. And the move is still
  completed in the same call, because a dataset left named in one place and mounted in another
  makes the jail undestroyable: `zfs destroy` answers `pool or dataset is busy`.

  A running jail loses that path at the unmount; the response says `remounted: true` so a
  caller knows the filesystem moved and not just the name.

  `<pool>/jails/retained` is created on demand and is **not** added to the layout readiness
  check: a host that has never retained anything is not missing something (JD-021).

- **Snapshots.** `GET /api/jails/{name}/snapshots` lists every snapshot of a jail's datasets;
  `PUT`, `DELETE` and `POST .../rollback` under `/snapshots/{snapshot}` take, destroy and roll
  back, with the same routes under `/datasets/{child}/` for one dataset alone. Every call is
  audited. (#4, JD-024)

  **A jail-level snapshot is recursive and atomic** — `zfs snapshot -r` takes the jail and
  every child at one point in time, which is what lets a root be re-cloned while its data
  dataset stays consistent with it (JD-007). **A rollback is never recursive**, because ZFS
  offers no way to make it so: `zfs rollback` cannot descend into children, and its own `-r`
  means "destroy the snapshots newer than this one". A jail-level rollback therefore leaves
  the children where they are.

  **Destroy and rollback take `confirm=`, and it is the snapshot's full `dataset@snapshot`** —
  not the word already in the path, so a script looping over jails with one snapshot name in a
  variable cannot confirm every one of them by accident (JD-016).

  **Refusals come before anything destructive runs.** A rollback that would destroy newer
  snapshots is `409` naming every one of them, until `destroy_newer=true` accepts losing
  exactly those. A clone or a hold blocks both a destroy and a rollback and cannot be
  overridden here at all. What blocks is asked for with `zfs get clones` and `zfs holds`,
  rather than read out of the error the destroy would have raised.

  Snapshots carry `create_txg`, ZFS's transaction group, and are ordered by it. `created` has
  second granularity and ties; the transaction group never does, and it is the ordering a
  rollback actually asks about.

- **A jail's datasets and tags can be written.** `PUT /api/jails/{name}/datasets/{child}`
  creates a child dataset or converges an existing one — `201` created, `200` already
  there, `409` it exists somewhere else. `PATCH /api/jails/{name}/properties` and
  `PATCH /api/jails/{name}/datasets/{child}/properties` set ZFS user properties, with a
  `null` value removing one via `zfs inherit`. Every write is audited with the command it
  ran, and carries the request's `X-Request-Id`. (#3, JD-023)

  **Five native properties are allowlisted** — `mountpoint`, `refquota`, `recordsize`,
  `compression`, `atime` — out of the sixty-odd this ZFS reports as editable. Anything else
  is `400` naming the list: `sharenfs` would publish a jail's filesystem over NFS and
  `canmount` would stop it mounting. `quota` is deliberately absent, because JD-007 puts the
  limit on `refquota`.

  `mountpoint` may be set only when creating. Changing it under a running jail would leave
  the jail looking at a directory no longer backed by that dataset, so asking for one an
  existing dataset does not have is `409` with both values rather than a move.

  **A value `zfs` refuses is `400`, not `500`**, carrying zfs's own sentence — a client told
  `500` would retry a value that can never work. The distinction available is only "ran and
  exited non-zero" against "never ran", so a server-side refusal such as permission denied
  also reports `400`; the message says which.

- **A jail listing carries its ZFS storage.** `GET /api/jails` and the new
  `GET /api/jails/{name}` include `datasets` — the dataset mounted at the jail's
  `path` and everything mounted under it, each with `name`, `mountpoint`, `used`,
  `referenced` and `available` — and `properties`, the ZFS *user* properties of the
  jail's own dataset. An automation client tags its jails with `otter:instance` and
  finds them again from the listing alone, with nothing stored in Jail Deck
  (JD-004, JD-011). Additive: both keys are absent for a jail that has neither.

  **The sizes are bytes**, not the `1.41G` that `zfs list` prints by default, so a
  client can compare and total them; the web interface formats them for display.
  `referenced` can exceed `used` on a jail cloned from a template, because a clone
  still refers to the template's blocks — the reason a quota belongs on a child
  dataset (JD-007).

  A jail is matched to its datasets **by mountpoint**, not by the dataset naming
  convention: a dataset is the jail's when it is mounted at the jail's path or
  under it. That holds for a jail placed outside `<pool>/jails/containers/<name>`,
  and reports nothing for a jail not on ZFS. A jail whose stanza sets no path
  matches nothing rather than everything.

  One batched `zfs list` and one batched `zfs get` answer every jail, never one
  call per jail, scoped to `JAILDECK_ZFS_POOL` when it is set and to every imported
  pool when it is not.

  **An unreadable pool does not fail the request.** The jails come back without
  their datasets and the reason is logged at `WARN`, because seeing a stopped jail
  is how an operator starts it (JD-004). The cost, accepted deliberately: absent
  storage can mean either "this jail has none" or "ZFS could not be read". (#2)

- **`X-Request-Id` is recorded in every audit entry a request produces**, and
  `GET /api/audit?request_id=<id>` filters on it, so an automation client can line
  its own job up against Jail Deck's audit trail (JD-016). Additive: an entry omits
  `request_id` entirely when the request sent no header, so a client that does not
  use it sees the shape it saw before.

  Up to 64 characters of ASCII letters, digits and `-._:` — a UUID, a ULID or a
  `trace:span` pair. Anything else is **dropped rather than rejected**, with the
  reason logged: the operation still succeeds and its entries carry no id. A
  correlation id is a convenience, and failing an operator's jail restart because
  their id held a space would be the worse outcome. The filter matches exactly and
  case-sensitively, since the id is the client's own opaque string and not a name
  Jail Deck normalises. (#14)

### Added

- **Templates and jail roots can be moved around from the interface.** The last of the
  write actions, which completes the pass: derive a template, remove one, restore a
  retained dataset, clone a jail as a template, and re-clone a jail's root. (JD-029)

  **A template snapshot is chosen, not typed.** Re-cloning takes the full
  `dataset@snapshot` with no default — every jail is already a clone of some template's
  `@base`, so defaulting to one would be a no-op dressed as an upgrade (JD-026) — but a
  string nobody can remember is not a choice either. The form offers the ready templates
  and sends the snapshot each one names.

  **A failed re-clone is rendered as the recovery procedure it is**: which step stopped,
  every dataset that is no longer under the jail and where it now sits, and that a restore
  brings it back. Nothing is retried and nothing is undone, so that message is the only
  record of where the data went.

  **Removing a template from its own page navigates back to the list**, because the thing
  the page was about is gone. A refusal stays, rendered against the dependents the page
  already lists.

  A restore puts a retained dataset under any jail under any name, which is what the API
  has always allowed and what makes "destroy the jail, keep the data" finish (JD-025).

- **Snapshots can be taken, rolled back to and destroyed from the interface.** (JD-029)

  **Taking one** covers the whole jail by default — one `zfs snapshot -r`, so every dataset
  is caught at the same instant (JD-024) — or one dataset alone. Taking one that already
  exists changes nothing and says so, which is what makes repeating a timed-out call safe.

  **Destroying one** asks for `confirm=<dataset>@<snapshot>` typed in full, because that is
  what the API asks for: `nightly` names a snapshot on every dataset that has one. At the
  jail's own dataset the destroy is recursive, and the confirmation **names every snapshot
  it would take** before anything runs.

  **A refusal is an answer, not a failure.** Nothing ran, so it is rendered under the row
  in the warning colour, naming the newer snapshots, the clones or the holds that stood in
  the way. A rollback refused over newer snapshots offers them back once — *destroy them
  and roll back* — because `destroy_newer=true` is the one refusal this API lets a client
  accept. Clones and holds get no such button: a hold exists to say "not this one", and
  taking a clone with its origin needs `zfs destroy -R`, which Jail Deck never passes.

- **A jail's tags and datasets can be changed from the interface.** The first of the write
  actions: add, edit and remove a ZFS tag; create a child dataset with its native
  properties; retain one past the jail. (JD-029)

  **A form opens where the thing is, and the modal is kept for what cannot be undone.**
  None of these destroys anything, so none of them gets a dialog. The dialog that does
  exist replaces the browser's `confirm()`, which could not carry the one thing the
  destructive operations need: the API will not destroy or roll back a snapshot without
  `confirm=<dataset>@<snapshot>` spelled out in full (JD-024), so the dialog asks for
  exactly that string and keeps its button disabled until it matches.

  **Nothing is validated twice.** A property name without a colon, a `recordsize` of 7K —
  both go to the server, and the server's own sentence comes back. The forms carry the
  rules as hints, because an operator should know before typing, but a hint that could
  disagree with ZFS is not a check.

  **Every write is followed by a read.** The server reads ZFS rather than remembering what
  it was told (JD-011), so a refetch is the only answer that cannot disagree with the host
  — and a command that failed can still have changed something.

  A 409 from the dataset endpoint is rendered from its body rather than as the word
  "Conflict": it carries the property, the value asked for and the value the dataset has,
  which is why the API answers that way instead of with a message.

- **The web interface reads the storage model.** Six releases of storage endpoints had
  nothing on screen; this is the reading half of catching up. (JD-028)

  **A page per jail** at `/jails/{name}`, behind the jail's name on the list: its datasets
  with the jail's own marked as such, its ZFS tags, and its snapshots newest first.

  **A Templates page** at `/templates` showing the lineage as an indented table — parent
  before child, ready or not — with the retained datasets below it, and a page per template
  at `/templates/{name}` listing what depends on it: the jails cloned from it, the templates
  derived from it, anything else, and holds.

  **What a clone is, is read from the host, not from the dataset's name.** Sorting
  dependents by looking for `/containers/` or `/templates/` in the path was tried and was
  wrong the first time it ran — `<pool>/jails/containers` is the layout Jail Deck creates,
  not where the jails it lists have to be. A clone is a jail when a jail reports that
  dataset as its own, and otherwise it is shown as the dataset it is.

  Nothing destructive is on these pages yet. The actions come next, each with the
  confirmation its refusal already defines.

- **A unix socket listener.** `JAILDECK_SOCKET` adds a second listener serving the
  same router, and `JAILDECK_SOCKET_ONLY=true` drops the TCP one, so a local
  automation client needs neither a port nor TLS (JD-002). One `http.Server` over
  both listeners, so they cannot drift apart in handler or timeouts.

  The socket is created mode `0660` by umask rather than chmodded afterwards: a
  listening socket queues connections from the moment it exists, so a chmod would
  leave a window in which the default `0755` was world-connectable. Its group is a
  first access check and not the only one — `RequireAuth` runs here exactly as on
  TCP, so the token is still required. `JAILDECK_SOCKET_GROUP` names the group;
  empty leaves it in the process's own, which for a root service on FreeBSD is
  `wheel`.

  Jail Deck installs no signal handler, so a stopped service leaves the socket file
  behind. The next start probes it: something answering means another instance, and
  it refuses rather than stealing its clients; nothing answering means a leftover,
  which it removes. A path holding a regular file is a configuration error and is
  never deleted. `JAILDECK_SOCKET_ONLY` needs `JAILDECK_SOCKET` set, and says so
  rather than starting with nothing to listen on. (#13)

- **An `rc.d` service script, `make install`, and one config file.** `make install`
  puts the binary in `$PREFIX/bin`, the script in `$PREFIX/etc/rc.d` and a sample
  config in `$PREFIX/etc/jaildeck` (`PREFIX` defaults to `/usr/local`, `DESTDIR`
  stages the layout elsewhere). After that,
  `sysrc jaildeck_enable=YES && service jaildeck start` runs Jail Deck as a
  service that survives a reboot.

  **Configuration is a file, not rc.conf knobs.** `/usr/local/etc/jaildeck/jaildeck.env`
  takes the same `KEY=value` lines as `.env.sample` and the same variables as the
  README's table, so one format configures Jail Deck whether it is started by hand
  or by `rc.d`. The script sources it with `set -a` and keeps only four knobs of its
  own, none of which are Jail Deck's configuration.

  Jail Deck runs in the foreground, so the script runs it under `daemon(8)` with
  `-f -S`: output goes to syslog under the tag `jaildeck`, which is what the log
  handler's missing timestamps were always for, and `-f` keeps `service jaildeck
  start` from hanging a remote shell. `-r` is deliberately not passed, so
  `service jaildeck stop` stops it and nothing puts it back.

## v0.7.0 — 2026-10-22

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.7.0)

### Breaking

- **Module path is now `github.com/ottermq/jaildeck`**, following the move back
  to GitHub. Builds that stamp the version with `-ldflags -X` need the new
  package path.

### Added

- **`GET /api/audit` pages backwards with a cursor.** `?before=<RFC 3339
  timestamp>` returns entries strictly older than the one given, newest first,
  and composes with the existing `operation`, `targets`, `success` and
  `timed_out` filters. Pass the timestamp of the oldest entry already received.

  The response stays a plain array, so a client knows there may be more the
  same way the interface does: a page that fills `limit` might not be the last
  one. An unreadable `before` is a `400` rather than being ignored, because a
  silently dropped cursor returns the first page again and a client paging
  backwards would loop on it.

  A cursor rather than an offset because the audit log is an append-only file
  that `newsyslog` rotates and new entries extend — both shift what an offset
  means between one request and the next, so `?offset=` can skip a page or
  repeat a row. (#67)

- **A "Load older" control on the operations page**, which appends the next
  page rather than replacing the table. (#67)

- **`GET /api/host/readiness` reports what the host can do, per capability.**
  Six checks — `kern.racct.enable`, `exec.timeout` in the jail.conf defaults,
  bridges, devfs rulesets, VT-x with EPT and UG, and PF — each answer `pass`,
  `fail` or `unknown`. Each one states what it observed and which capability
  it gates (`jails`, `limits`, `vnet`, `provisioning`, `vms`, `firewall`).
  Every failed check carries a remedy, written as a command to paste into a
  root shell. Nothing in the path writes to the host.

  Readiness is per capability because "is this host ready" has no single
  answer: VT-x matters only to someone who wants VMs. A capability is
  `unavailable` if any check gating it fails, `unknown` if any is unknown, and
  `ready` otherwise. The report carries both levels so no client reimplements
  that rule.

  `unknown` is a third answer, not a softer `fail`, and carries no remedy. A
  kernel without RACCT has no `kern.racct.enable` to set, so the loader.conf
  advice that fixes a `0` would be wrong there. A host that is not x86 has no
  VT-x line to find, and an absent line is not proof the CPU lacks it. A wrong
  `fail` sends the operator to fix something that may be fine.

  The rules were written against real output from four hosts, committed under
  `internal/system/freebsd/testdata/hostfacts`, and three of them changed
  because of it. The VT-x line is read from `/var/run/dmesg.boot`, because the
  live `dmesg` buffer wraps on a long-running host and loses it. devfs
  rulesets come from the kernel: no captured host has `/etc/devfs.rules`, yet
  every one has rulesets 1–5 loaded. And a host need not have `/etc/jail.conf`
  at all. (#1)

- **`jaildeck init` and `PUT /api/host/layout` ensure the jails dataset
  layout:** `<pool>/jails` mounted at `/usr/local/jails`, with `media`,
  `templates` and `containers` beneath it. Both call one service method, and
  `GET /api/host/layout` reports each dataset, its mountpoint and what is
  missing. The readiness report gains `zfs_pool` and `zfs_layout`, which gate
  `provisioning`.

  Ensure, never clobber. When everything is in place no `zfs create` runs and
  the answer is `200`. When something is missing, only that is created, parent
  first, and the answer is `201`. A dataset that exists but is mounted
  somewhere else is a `409` that names it, and nothing is created: fixing it
  would move a mounted dataset that may hold live jails. So is a missing
  dataset whose path another dataset already holds, on any pool, since
  creating it would mount it over that one. `PUT` rather than
  `POST` because the layout is a singleton whose desired state is fixed
  (JD-016), and there is no `confirm` parameter because nothing here destroys.
  Each `zfs create` is its own `create-dataset` audit entry, findable with
  `?targets=<dataset>`.

  A host with one pool has it adopted. With several, `JAILDECK_ZFS_POOL` must
  name one, and until it does the `zfs_pool` check fails, listing the pools.
  Jail Deck does not pick: on the deployment host the first pool printed holds
  a replica of the layout, mounted nowhere. A missing or ambiguous pool fails
  the check but never stops Jail Deck from starting, since listing and
  running jails do not need the layout. A host with no ZFS fails it too,
  detected by `zpool` exiting non-zero, because the binary is in base even
  where ZFS is not. (#21)

- **A Host page shows the readiness report**, capability by capability, each
  with the checks that gate it. It leads with what is ready. An unavailable
  capability is drawn neutral, not red, because a host without VT-x is a
  perfectly good jail host. `unknown` is amber, like a timed-out operation,
  and never reads as a failure: a wrapped `dmesg` buffer is not a CPU without
  VT-x. Every remedy is shown exactly as the server sends it, as code to
  paste into a root shell, with a copy button where the browser allows one.
  The report is recomputed per request (JD-011), so the page keeps nothing
  between loads and has a Refresh control instead. It has no button for
  `PUT /api/host/layout`: a control that changes the host needs its own
  decision. (#22)

### Fixed

- **The non-loopback startup warning now says that login will not work.** It
  named the real risk — the operator password crossing the network in
  plaintext — but not the consequence an operator meets first. The session
  cookie is `Secure` (JD-003), so a browser on a plain-HTTP origin discards
  it: the login returns `200` and every request after it returns `401`, which
  looks exactly like a rejected password. The two `401`s are told apart only
  by their bodies. The README now says the same where it explains the port
  forward. (#27)

## v0.6.0 — 2026-10-21

[Release notes](https://codeberg.org/Otter-Labs/jaildeck/releases/tag/v0.6.0)

### Breaking

- **The JSON API moved under `/api`.** Every path changed: `/jails` is now
  `/api/jails`, `/audit` is `/api/audit`, `/auth/login` is `/api/auth/login`.
  Clients need their base URL updated — appending `/api` is enough, which is
  all the bundled Insomnium collection required. The root path is being handed
  to the web interface (#58), so the API needed a namespace of its own. (#57)

- **`GET /api/jails` now uses JSON tags.** The `Jail` type had no JSON tags, so
  it emitted Go field names: `JID`, `Name`, `Status`, `Hostname`, `IP`, `Path`.
  It now emits `jid`, `name`, `status`, `hostname`, `ip`, `path`, matching every
  other type the API returns. The jail returned by `POST /api/jails/{name}/start`,
  `/stop` and `/restart` changes the same way. (#60)

### Added

- **A web interface.** The binary now serves a Svelte single-page application
  at `/`; the JSON API stays under `/api`. The frontend is embedded via
  `embed.FS`, so there is still one binary and no Node.js at runtime — building
  it is `make web`, and a clone without that step compiles and runs, serving a
  page that says so. (#58)

- **The jails list, with `start`, `stop` and `restart`.** `/jails` lists every
  jail on the host with its JID, hostname, IP and path, and starts, stops or
  restarts them from the browser; stop and restart confirm first. An operation
  can run to its 150-second backstop (JD-014), so the row shows progress and
  refuses a second click until it returns.

  Two behaviours worth knowing. A failure shows the command's own diagnostic
  rather than a generic message. And a **timed-out** operation is reported as
  *may still be running* rather than as a failure — Jail Deck stopped waiting,
  which is not the same as the jail failing to start — with the list refetched
  so it shows the jail's actual state. (#60)

- **The operations view.** `/operations` lists what Jail Deck has run, newest
  first, with the command that actually executed and filters for target,
  operation and outcome. A **timed-out** operation is shown as its own outcome
  rather than as a failure: the API distinguishes the two deliberately, and
  before v0.4.0 both reported exit code `-1`. (#61)

### Fixed

- **`GET /api/audit` returns `[]` rather than `null` when nothing matches.** A
  fresh install with no audit file, and any filter matching no entries, both
  returned JSON `null`, so every client had to handle two shapes for "no
  results". `GET /api/jails` already returned `[]`. (#61)

- **`jail.conf` shared defaults are honoured.** Settings outside any jail block
  apply to every jail below them, which is the layout the FreeBSD handbook
  recommends: shared defaults in `/etc/jail.conf`, one small file per jail in
  `/etc/jail.conf.d/`. Jail Deck read only what sat inside a `name { … }` block,
  so a jail configured that way reported an empty `Path` — and its hostname was
  correct only by coincidence, since the fallback happens to be the jail's own
  name. A `${name}` in a shared default is expanded per jail rather than once.
  Running jails were unaffected because `jls` supplies the real values, so this
  was visible only for stopped ones. (#51)

- **`ip4` is no longer reported as a jail's address.** It names a mode —
  `inherit`, `new` or `disable` — while addresses live in `ip4.addr`. A jail
  configured with `ip4 = inherit` showed the literal string `inherit` where its
  IP should be; it now shows none, which is also what `jls` reports for it. (#51)

## v0.5.2 — 2026-10-07

[Release notes](https://codeberg.org/Otter-Labs/jaildeck/releases/tag/v0.5.2)

### Changed

- **The request log is now structured, and on stderr.** It previously came from
  chi's own middleware as free-form lines on stdout; it now goes through
  `log/slog` alongside every other record — one record per request, with the level
  taken from the status: 5xx `ERROR`, 4xx `WARN`, otherwise `INFO`. Anything
  reading stdout for request lines will find nothing there. (#44)
- **The startup line is structured**: `msg=listening version=… addr=…`, in place
  of a prose sentence.

### Added

- `principal_kind` on every authenticated request record, and `principal` when the
  credential has a name — a token's name, the one `jaildeck token revoke` takes. A
  session has no name; there is one operator. (#44)
- Records emitted while serving a request now carry that request's `method`,
  `path` and principal. A failed jail command's DEBUG record therefore says which
  request and which credential produced it. (#44)

### Fixed

- The startup record was malformed. `slog.Info` was called with a format string
  and positional arguments, so slog paired them as an attribute and the version
  became the *key*: `msg="Jail Deck %s listening on %s" v0.5.1=127.0.0.1:8888`.

### Known issues

- **`jail.conf` shared defaults are ignored.** A stopped jail configured the way
  the FreeBSD handbook recommends — shared defaults in `/etc/jail.conf`, one file
  per jail in `/etc/jail.conf.d/` — reports an empty `Path`, and `ip4 = inherit`
  surfaces as the literal string `inherit`. Running jails are unaffected, because
  `jls` supplies the real values. (#51)

## v0.5.1 — 2026-10-04

[Release notes](https://codeberg.org/Otter-Labs/jaildeck/releases/tag/v0.5.1)

### Changed

- **Log output is now structured** — `log/slog` with a text handler, on stderr,
  one record per line. Anything parsing the previous free-form output needs
  adjusting. The timestamp is omitted deliberately: `daemon(8)` and syslog add
  their own. (#31)

### Added

- `JAILDECK_LOG_LEVEL` — `debug`, `info`, `warn`, `error`; default `info`. An
  unrecognised value falls back to `info` and says so rather than refusing to
  start. (#31)
- A failed audit write is logged at `ERROR` with the operation, target and
  underlying error. It previously went to stderr through the standard `log`
  package, which under `rc.d` means wherever the service script sends it —
  usually nowhere, so a lost audit record was silent. (#31)
- DEBUG diagnostics in `internal/system`: every command's exit code, duration
  and stderr; the raw `jls` output when its JSON will not parse; any `jail.conf`
  line the parser could not understand; any value still containing `$` after
  substitution. (#31)
- `component=` on every record, naming the part of Jail Deck that produced it.
  (#31)

### Fixed

- `jail.conf` paths using the bare `$name` form are resolved. Only `${name}` and
  `$(name)` were handled, so `GET /jails` reported a literal `"/jails/$name"` —
  a wrong value rather than a missing one. (#43)
- A `#` inside a quoted `jail.conf` value no longer truncates the line.
  `host.hostname = "a#b";` was cut at the `#` and discarded, after which the
  hostname silently fell back to the jail's own name. Single-quoted values and
  escaped quotes are handled too. (#43)

Both of those were found by the DEBUG diagnostics added above, not by reading
the code.

## v0.5.0 — 2026-10-01

[Release notes](https://codeberg.org/Otter-Labs/jaildeck/releases/tag/v0.5.0)

Mandatory single-operator authentication. The Access Control milestone.

### Breaking

- **Jail Deck will not start without a credential.** Run `jaildeck passwd`
  before upgrading a running install. There is no unauthenticated mode, on any
  bind address, including loopback — a jail with `ip4=inherit` shares the host's
  loopback, so localhost was never a private channel here. (#13, JD-003)
- **Every route requires a credential.** Existing API scripts receive `401`
  until they authenticate with a session cookie or a bearer token. (#15)
- **The audit log moved to `/var/log/jaildeck/audit.log`**, from a
  working-directory-relative path that an `rc.d`-started service wrote to `/` or
  lost entirely. The old file is not read. (#28)

### Added

- Session authentication: argon2id credential, opaque server-side session
  tokens, `HttpOnly`/`Secure`/`SameSite=Strict` cookie with an explicit `Path`
  and a 12-hour `MaxAge`. Not JWT — immediate revocation matters more here than
  stateless verification. (#14, JD-003)
- `jaildeck passwd` and `jaildeck passwd --stdin`, the only writer of the
  credential. Never accepts the password as an argument, so it cannot reach
  shell history. There is no recovery flow: root runs it again. (#12)
- `jaildeck token add|list|revoke` — long-lived API tokens for scripting, as a
  credential distinct from the browser session. Hashed with SHA-256, not
  argon2id: stretching compensates for low entropy, and a 256-bit random value
  has none to buy back. Revocable by name. (#16)
- `POST /auth/login`, `POST /auth/logout`, `GET /auth/me`. (#14)
- An application state file at `/var/db/jaildeck/state.json`, mode `0600`,
  written atomically — temp file, `fsync`, `rename`, `fsync` of the directory.
  (#11, JD-011)
- A startup warning when bound non-loopback, naming both supported paths: a
  reverse proxy terminating TLS, or an SSH `LocalForward`. It warns rather than
  refusing — a proxy cannot be detected from inside the process. (#17, JD-002)
- `GET /audit?timed_out=` filtering. (#20)
- `JAILDECK_STATE_DIR` and `JAILDECK_AUDIT_LOG`.

### Fixed

- Explicit HTTP server timeouts. `WriteTimeout` stays unset deliberately: it
  would cover handler execution, and a jail operation may legitimately run to
  its 150-second backstop. (#18, JD-014)
- Audit entries record the command that actually ran, rather than a
  reconstruction of it. (#21)
- State-file decode errors name the file.

### Notes

- Changing the password invalidates browser sessions but **not** API tokens.
  Sessions are bound to the credential they were issued against; a token is an
  independent credential, revoked explicitly. (JD-003)
- Sessions are held in memory and do not survive a restart.

## v0.4.0 — 2026-09-16

[Release notes](https://codeberg.org/Otter-Labs/jaildeck/releases/tag/v0.4.0)

The API-only release. The server-rendered HTML was removed in favour of a JSON
API; the Svelte replacement has not been built.

### Breaking

- Server-rendered HTML removed. The backend is JSON-only. (JD-009)
- Module path is now `codeberg.org/Otter-Labs/jaildeck`, following the move from
  GitHub.
- An empty `JAILDECK_HOST` no longer binds all interfaces; the default is
  `127.0.0.1`. (JD-002)
- `GET /audit?operation=` matching is case-insensitive, and any value is
  accepted — an operation never logged returns an empty result rather than the
  unfiltered list.
- Jail operations no longer abort when the client disconnects. (JD-014)
- Timed-out operations return `504`, not `500`.

### Added

- `jaildeck version`, and the version in the startup log.
- Command timeouts on every command, with a jail-operation backstop sized above
  FreeBSD's own `exec.timeout` and `stop.timeout`. Commands are terminated as a
  process group. (JD-014)
- `timed_out` on audit entries, distinguishing "we stopped waiting" from "the
  command failed" — both previously reported exit code `-1`.
- `JAILDECK_ADAPTER=freebsd|fake`, selecting the in-memory adapter by
  configuration instead of by editing source.
- `collections/insomnium.json`, an API collection for driving the JSON API.

## v0.3.1 — 2026-07-08

Tag only; no release.

### Fixed

- Logging and error handling in jail operations.

## v0.3.0 — 2026-07-08

Tag only; no release.

### Added

- Operation result handling and UI feedback for jail actions.

## v0.2.0 — 2026-07-07

Tag only; no release.

### Added

- Operation logging.

## v0.1.0 — 2026-07-06

Tag only; no release.

### Added

- Jail listing and lifecycle management.
