# Host facts — real command output, captured from real FreeBSD hosts

Raw output of every command the Host Setup readiness report and dataset layout parse
(#1, #21). Each capture is three files, because all three carry signal:

| | |
| --- | --- |
| `<name>.stdout` | stdout, byte for byte |
| `<name>.stderr` | stderr |
| `<name>.exit` | exit status |

(Not `.out` — the repository's `.gitignore` excludes `*.out` as a Go coverage artifact,
which would quietly leave every capture untracked.)

PF makes the case for keeping all three. Not loaded, loaded-and-disabled and
loaded-and-enabled are three different answers, and no single file tells them apart:

| state | `kldstat -m pf` | `pfctl -s info` | `service pf status` |
| --- | --- | --- | --- |
| not loaded | exit 1 | exit 1, nothing on stdout | exit 1, `pf.ko is not loaded` |
| loaded, disabled | exit 0 | **exit 0**, `Status: Disabled for …` | exit 1, `Status: Disabled …` |
| loaded, enabled | exit 0 | exit 0, `Status: Enabled for …` | exit 0, `Status: Enabled …` |

`pfctl -s info` exits 0 whether PF is enabled or not, so the exit status alone cannot
distinguish disabled from enabled; `service pf status` exits 1 both when PF is absent and
when it is merely disabled, two states whose stdout has nothing in common. One command
answers both questions: run `pfctl -s info`, treat a non-zero exit as "not loaded", and
read `Enabled`/`Disabled` out of the `Status:` field otherwise.

`MANIFEST` lists every command with the exit status it returned.

## Why these are committed

Parsing is the one part of the FreeBSD adapters that cannot be written against the fake
adapter. The fake exists so handlers, services and the web interface can be developed
without a FreeBSD host; it says nothing about what `zfs list` actually prints. Hand-typed
fixtures are guesses, and guesses here produce a parser that is confidently wrong against
the real thing — see "What the real output corrected" below, where a check that had
already been specified turns out to report `fail` on two hosts that pass it.

The hosts that are *not* the deployment target earn their place by reaching code paths
`isengard` is not in **today**. `rohan`'s wrapped `dmesg` buffer is the clearest case:
that bug is latent on isengard too and needs nothing but uptime to surface. `rivendell`'s
missing sysctl oid is the same — jaildeck will never run on armv7, but "this oid does not
exist" is reachable on any host. Pruning those two sets on the grounds that only isengard
matters would delete the fixtures that cover isengard's future.

## Re-capturing

`capture.sh` is read-only; it changes nothing on the host. It runs no pipelines either,
because `system.CommandRunner` takes a name and an argument array and starts no shell —
the adapter cannot pipe, so a capture that greps captures something the adapter can never
see. Run it on any FreeBSD box and copy the result into a directory named after the host:

```sh
ssh <host> 'sh -s' < capture.sh
scp -r <host>:/tmp/jd-capture ./<hostname>
```

Its one edit to a captured byte is to mask MAC addresses, which `dmesg` prints for every
NIC. Nothing any parser reads is affected and the substitution is in the script, so a
re-capture reproduces these files.

`capture-induced.sh` and `capture-layout-induced.sh` sit beside it for the states a host
is not normally in. Those two **change the host** and restore it afterwards — see the
induced sets below.

## Hosts

### `vm-15.1/` — 15.1-RELEASE-p3, a libvirt VM, captured 2026-10-22

The laptop's throwaway VM (`freebsd-jails`, ssh alias `fbsd`). One pool, one jail, and
`/etc/jail.conf` laid out the way the handbook suggests: shared defaults at the top,
`exec.timeout = 120` among them, then `.include "/etc/jail.conf.d/*.conf";`.

It is a KVM guest, so its `VT-x` line describes nested virtualisation.

### `isengard/` — 15.1-RELEASE-p5, bare metal, captured 2026-10-22

The target host: a Xeon E5620, three pools, three running jails, reached over Tailscale.
A laboratory like the VM — nothing on it is precious and it can be rebuilt. This is the authority for anything the VM cannot answer honestly — the `VT-x`
line in particular — and it disagrees with the VM in three ways that matter:

- **No `/etc/jail.conf`.** Only `/etc/jail.conf.d/{goodiesdb,ottermq,postgres}.conf`,
  each stanza self-contained, and **none of them sets `exec.timeout`**. A host can run
  jails with no shared defaults anywhere.
- **Three pools** — `backup`, `tank`, `zroot`, printed in that order. The layout is on
  `tank`.
- **`racct` is enabled**, from `kern.racct.enable=1` in `/boot/loader.conf`.

Its layout matches the VM's — `tank/jails` with `containers`, `media` and `templates`.
The first capture of this host was missing `media`, because it had been rebuilt and the
dataset was not recreated; it was added on 2026-10-22 and re-captured. Worth remembering
as a fact about real hosts rather than a quirk of this one: a layout can be *partly*
there, which is the ordinary state of a host mid-setup and the reason the layout check
reports per dataset instead of yes or no.

### `rohan/` — 15.1-RELEASE-p2, bare metal, captured 2026-10-22

A Haswell i5-4278U with two pools (`zroot`, and `media` mounted at `/srv/media`), and the
set the other two cannot give: **an unprovisioned host.** No jails dataset on either pool,
no jails running, no `/etc/jail.conf`, `racct` off. This is the state the readiness report
exists to describe and #21's create path is for — everything before it was captured on a
host that was already set up.

It contributes two traps of its own:

- **It has been up 83 days, and its kernel message buffer has wrapped.** See the
  `dmesg.boot` entry below; this host is the reason that mechanism changed.
- **A pool is named `media`**, which is also one of the four dataset names the layout
  check looks for. `media` the pool mounts at `/srv/media`; `<pool>/jails/media` is a
  different thing entirely. Match full dataset paths, not name fragments.

### `rivendell/` — 15.1-RELEASE, armv7, captured 2026-10-22

A Raspberry Pi 2: Cortex-A7, `hw.machine_arch` **armv7**, root on **UFS with no ZFS at
all**. Reached through `rohan`, since it has no Tailscale package. It is not a jail host
and never will be, which is exactly why it is here — it answers the questions the x86
boxes cannot:

- **the only genuine `unknown`.** `sysctl -n kern.racct.enable` exits 1 with
  `sysctl: unknown oid 'kern.racct.enable'`: this kernel has no RACCT at all, which is
  neither pass nor fail.
- **no `VT-x:` line anywhere** — 0 matches in 172 lines of `dmesg.boot`. The state the
  `cpu_virtualization` check previously had no bytes for.
- **no ZFS**, which is what `zfs_pool` exists to report and what JD-006 requires.
- **an empty `/etc/jail.conf.d/`** — the directory exists and holds nothing.

`CPU Features:` is present and **empty** on this host. A parser that finds the line and
assumes it has content gets an empty token list, not an error.

### `vm-15.1-induced/`, `isengard-induced/` — states no host was in

Several checks had no passing capture anywhere, which left their success paths written
against nothing: a bridge, PF loaded, PF enabled, `vmm` loaded. `capture-induced.sh`
produces them — it loads the modules, creates the interfaces, captures, and puts
everything back, printing what it restored. Both hosts are laboratories, so it is
committed next to `capture.sh`, with the difference stated in its header: **this one
changes the host.** Run it on a box whose state does not matter, and nowhere else.

```sh
ssh <host> 'sh -s' < capture-induced.sh
scp -r <host>:/tmp/jd-capture-induced ./<hostname>-induced
```

Both hosts produced the same shapes, including `vmm` — the VM's nested virtualisation is
enough to load it. MACs are masked as in the other sets; a bridge is given one at
creation anyway.

`vm-15.1-induced/` also holds the `layout_*` captures from `capture-layout-induced.sh`,
run on 2026-10-22 for #21: the jails layout **partly there** (`media` renamed aside),
**present but mounted elsewhere** (`media` at `/mnt/jd-capture-misplaced`), and the bytes
of the write path — `zfs create` succeeding (exit 0, nothing on either stream) and
failing because the dataset exists (exit 1,
`cannot create 'zroot/jails/media': dataset already exists`). The script refuses to start
unless the layout is complete, destroys only the replacement it created and marked, and
renames the original back; on the VM the layout afterwards matched
`vm-15.1/zfs_list_r_zroot` byte for byte. Laboratory hosts only, like the other one.

```sh
ssh <host> 'sh -s [pool]' < capture-layout-induced.sh
scp '<host>:/tmp/jd-capture-layout-induced/layout_*' ./<hostname>-induced/
```

## Where each check stands

| Check | `vm-15.1` | `isengard` | `rohan` | `rivendell` | induced |
| --- | --- | --- | --- | --- | --- |
| `exec_timeout` | **pass** — `= 120` | **fail** — no `jail.conf` | **fail** — no `jail.conf` | **fail** — none, and `jail.conf.d` empty | |
| `racct` | **fail** — `0` | **pass** — `1` | **fail** — `0` | **unknown** — oid absent | |
| `bridges` | **fail** — `vtnet0 lo0` | **fail** — 4 ifaces | **fail** — 3 ifaces | **fail** — `lo0 ue0` | **pass** — one, then two |
| `devfs_rulesets` | **pass** — `1 2 3 4 5` | **pass** | **pass** | **pass** — arch-independent | |
| `cpu_virtualization` | **pass**, nested | **pass**, Westmere | **pass** from `dmesg.boot`, **unanswerable** from `dmesg` | **n/a** — armv7, no line | |
| `pf` | **fail** — not loaded | **fail** | **fail** | **fail** | **pass** — disabled, then enabled |
| `vms` | **fail** — no `vmm` | **fail** | **fail** | **fail** — no `hw.vmm` oid | **loaded** — `hw.vmm` appears |
| `zfs_pool` | one pool, `zroot` | **three pools** — cannot be guessed | two pools, one named `media` | **no ZFS at all** | |
| `zfs_layout` | **pass** | **pass** on `tank`; `backup` holds a decoy | **nothing provisioned** | **n/a** | **partial**, **misplaced** |

### Not covered, by decision

Two states have no capture and are not going to get one, because both need hardware that
does not exist on this network:

- an **AMD** host, which reports `SVM:` instead of `VT-x:`. All three x86 hosts here are
  Intel — Westmere, Haswell and a KVM guest.
- `VT-x: (disabled in BIOS)`, which needs a machine whose firmware has it switched off.

**This is an end state, not a backlog.** `isengard` is the deployment target, and the
report is built to answer `unknown` — with no remedy attached — on anything it does not
recognise. That is the correct answer on an unseen host: a wrong `fail` sends someone to
reboot a working machine. If jaildeck runs somewhere these fixtures do not describe and
gets a check wrong, the fix starts with an issue, not with a guess here.

**`capture.sh` is the bug report.** It is read-only, runs on any FreeBSD host, and emits
exactly the bytes needed to write the parser fix:

```sh
ssh <host> 'sh -s' < capture.sh
scp -r <host>:/tmp/jd-capture ./<hostname>
```

Attaching that directory to the issue turns "it does not work on my machine" into a new
fixture set and a one-commit fix. Without it, a report about an unrecognised host is
guesswork again — which is the thing this directory exists to prevent.

The write path is a different matter: still unwritten rather than unavailable, except for
`zfs create`, which `capture-layout-induced.sh` now covers. PF with an actual ruleset
loaded is still missing. (`pfctl -s rules` with PF enabled
and no `pf.conf` is **exit 0 and empty stdout** — the no-rules case, not an error.)

## What the real output corrected

Every one of these was wrong in a hand-written fixture, or in the specification written
before the captures existed:

- **`devfs_rulesets` was specified backwards.** It read `/etc/devfs.rules`, and the plan
  was to call the file's absence a `fail` with a remedy. Neither host has that file, and
  both have rulesets `1 2 3 4 5` loaded, because `/etc/defaults/devfs.rules` ships
  `[devfsrules_jail=4]` and `[devfsrules_jail_vnet=5]` — the very ruleset JD-018 puts in
  the jail spec. The check would have told the operator to install what was already
  there. **The kernel is the authority: `devfs rule showsets` for the numbers**, the files
  only for the names. `showsets` is root-only; unprivileged it exits 1 with
  `devfs rule: ioctl DEVFSIO_SGETNEXT: Operation not permitted` on stderr
  (`devfs_rule_showsets_unpriv`), which is an `unknown`, while exit 0 with empty stdout
  would be a genuine "none".
  The first capture hid this: it ran `cat /etc/defaults/devfs.rules` unprivileged, got
  `Permission denied`, and never saw the shipped rulesets.
- **`/etc/jail.conf` need not exist**, even on a host running jails. `exec_timeout` has to
  treat an empty defaults map as a legitimate `fail`, and its remedy cannot say "edit the
  defaults block" when there is no file to edit. (`listJailsFromConfFile` already returns
  `nil, nil` for a missing file, so listing jails still works off `jail.conf.d`.)
- **The first pool is not the pool.** `zpool list` prints `backup` first on `isengard`,
  and `backup` holds a `zrepl` replica — `backup/isengard/tank/jails`,
  `…/jails/containers`, `…/jails/templates/15.1-RELEASE` — with **every mountpoint
  `none`**. A layout check matching on dataset names would call the backup pool a
  provisioned host. Hence an explicit `JAILDECK_ZFS_POOL`, a `zfs_pool` check that lists
  every pool and chooses none, and a layout check that compares **mountpoints**.
  That replica is **not** #21's `409`, though it looks like one: it lives at
  `backup/isengard/tank/jails`, not at `backup/jails`, so with `JAILDECK_ZFS_POOL=backup`
  the layout is simply missing. The danger there is the opposite one — creating
  `backup/jails` would mount it over `tank/jails`, where the jails run — so the layout
  check also refuses to create a dataset whose path another dataset already holds. The
  real `409` is in `vm-15.1-induced/layout_misplaced_zfs_list`.
- **The live `dmesg` buffer wraps; `/var/run/dmesg.boot` does not.** This changed the
  mechanism, not just the parsing. `rohan` has been up 83 days and its `VT-x:` line is
  **gone from `dmesg` altogether** — 0 matches in 1651 lines — while `dmesg.boot` still
  carries it at line 18. A check that runs `dmesg` answers `unknown` on a perfectly
  capable host for no reason but uptime, and `unknown` was chosen *because* the buffer
  wraps, so the check would have been permanently unable to answer on exactly the
  long-running hosts it matters for. Read `/var/run/dmesg.boot` — mode 0644, written once
  by `/etc/rc.d/dmesg`, no root and no module — and fall back to `dmesg` only when the
  file is absent. Both are captured, so the difference stays visible.
  There is no better source. FreeBSD exposes no sysctl for the VMX/SVM CPUID bits:
  `fastfetch` reads `hw.model`, `hw.ncpu` and `kern.smp.cores` for the CPU and gets
  feature bits from CPUID directly, which from userland means `/dev/cpuctl` — and
  `cpuctl.ko` is not loaded on any of these hosts, so reaching it is a write. The one
  related sysctl, `kern.vm_guest`, answers the opposite question — am I running *inside* a
  hypervisor. It reads `none` on both bare-metal hosts and `kvm` on the VM, which is a
  reliable fact and a useful one for JD-019, but it says nothing about whether a host can
  *run* guests.
- **The `VT-x:` line is indented two spaces.** `strings.HasPrefix(line, "VT-x:")` finds
  nothing on either host. Trim first.
- **Its feature list varies by CPU**, so features must be split on `,` and compared as
  whole tokens. Bare metal here is `PAT,HLT,MTF,PAUSE,EPT,UG,VPID`; the KVM guest reports
  *more* (`…,VPID,VID,PostIntr`), since the Westmere Xeon has no APICv. Never
  `strings.Contains` over the line, and never over the buffer: `UG` is a substring of
  `root HUB`, which `dmesg` prints once per USB controller — sixteen times on `isengard`.
  `VMX` is no safer; it appears in the unrelated `Features2=…<…,VMX,SMX,…>` line.
- **`dmesg` cannot be piped.** `system.CommandRunner` runs no shell, so there is no
  `dmesg | grep`: read the whole buffer and filter in Go.
- **`ifconfig -g bridge` with no bridges exits 0 with empty stdout** — not an error. With
  bridges it prints one name per line.
- **`zfs list` naming several datasets where one is missing exits 1 and still prints the
  others on stdout.** Treating a non-zero exit as total failure silently loses the
  datasets that do exist. Hence `-r <pool>`, one call, match in Go.
- **A pool's own mountpoint can be `none`** (`zroot`'s is, and every dataset on `backup`),
  so the layout check must not assume every dataset has a filesystem path.
- **`hw.vmm` exists only while `vmm.ko` is loaded, and its vendor subtrees lie.** Before
  the load, `sysctl hw.vmm` exits 1 with `sysctl: unknown oid 'hw.vmm'`. After it, the
  tree carries `hw.vmm.vmx.*` **and** `hw.vmm.svm.*`/`hw.vmm.amdvi.*` on an Intel host,
  where `hw.vmm.svm.features` reads `4294967295` — `0xffffffff`, a not-applicable
  sentinel, not a feature mask. Never infer the CPU vendor from which subtree exists.
  `hw.vmm.vmx.initialized: 1` is the real "bhyve can run here" signal, for JD-019 — but
  loading a kernel module is a write, and the #1 report is read-only, so `dmesg` stays
  the mechanism there.
  The two interfaces agree, which is the best evidence these fixtures offer that the
  `dmesg` parsing is right: `hw.vmm.vmx.cap.unrestricted_guest` is `1` on both hosts,
  matching the `UG` token, and `hw.vmm.vmx.cap.virtual_interrupt_delivery` is `1` on the
  VM and `0` on `isengard` — exactly where `VID` appears and does not appear in each
  host's `VT-x` line. `/dev/vmm` does not exist until a VM is created, so its absence
  says nothing.
- **`racct` has three kernel states, not two, and the remedy differs.** `sysctl -n
  kern.racct.enable` returns `1` (isengard), `0` (the VM, rohan), or **exits 1 with
  `sysctl: unknown oid 'kern.racct.enable'`** (rivendell — the kernel has no RACCT at
  all). `rctl` names each one: exit 0 when enabled; `RACCT/RCTL present, but disabled;
  enable using kern.racct.enable=1 tunable`; and `RACCT/RCTL support not present in
  kernel; see rctl(8) for details`. The distinction is not cosmetic — telling the
  operator to add `kern.racct.enable=1` to `/boot/loader.conf` is correct for the second
  and **wrong** for the third, where no tunable exists and the kernel itself lacks the
  feature. That third state maps to `unknown`, which needs no remedy, so the rule already
  avoids the bad advice; `rctl`'s message is how the detail field stays accurate.
- **"No ZFS" is a module-load failure, not a missing binary.** `zpool` and `zfs` are in
  base on every FreeBSD host, arch regardless. On rivendell they exist and fail:
  `zpool list` exits 1 with `Failed to load zfs module: Operation not permitted`, while
  `kldstat -m zfs` exits 1 with `can't find module zfs: No such file or directory` —
  two different messages for one fact. Anything keying on `exec.ErrNotFound` or
  "command not found" will not notice that ZFS is absent.
- **`/etc/jail.conf.d/` can exist and be empty**, which makes three distinct jail-config
  shapes across the fixtures: `jail.conf` with shared defaults plus an include (the VM),
  no `jail.conf` and a populated `jail.conf.d` (isengard), and no `jail.conf` with an
  empty `jail.conf.d` (rivendell, where the glob comes back unexpanded as
  `cat: /etc/jail.conf.d/*.conf: No such file or directory`).
