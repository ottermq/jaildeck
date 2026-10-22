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

`capture-induced.sh` sits beside it for the states a host is not normally in. That one
**changes the host** and restores it afterwards — see the induced sets below.

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

## Where each check stands

| Check | `vm-15.1` | `isengard` | `rohan` | induced |
| --- | --- | --- | --- | --- |
| `exec_timeout` | **pass** — `exec.timeout = 120` | **fail** — no `jail.conf` | **fail** — no `jail.conf` | |
| `racct` | **fail** — `0` | **pass** — `1` | **fail** — `0` | |
| `bridges` | **fail** — `vtnet0 lo0` | **fail** — `em0 em1 lo0 tailscale0` | **fail** — `bge0 lo0 tailscale0` | **pass** — one bridge, then two |
| `devfs_rulesets` | **pass** — `1 2 3 4 5` | **pass** — `1 2 3 4 5` | **pass** — `1 2 3 4 5` | |
| `cpu_virtualization` | **pass**, nested | **pass**, Westmere | **pass** from `dmesg.boot`, **unanswerable** from `dmesg` | |
| `pf` | **fail** — not loaded | **fail** — not loaded | **fail** — not loaded | **pass** — disabled, then enabled |
| `vms` | **fail** — `vmm` not loaded | **fail** — `vmm` not loaded | **fail** — `vmm` not loaded | **loaded** — `hw.vmm` appears |
| `zfs_pool` | one pool, `zroot` | **three pools** — cannot be guessed | two pools, one named `media` | |
| `zfs_layout` | **pass** — `zroot/jails` → `/usr/local/jails` | **pass** on `tank`; `backup` holds a decoy | **nothing provisioned** | |

### Still missing

- any check in the **`unknown`** state other than a privilege error
- `VT-x: (disabled in BIOS)`, and a host with no `VT-x` line at all
- an **AMD** host: it reports `SVM:` instead of `VT-x:`, and all three hosts here are
  Intel — Westmere, Haswell and a KVM guest. Nothing reachable can produce `SVM:`.
- a **non-x86** host, where there is no `VT-x:`/`SVM:` line at all and the check does not
  apply. `hw.machine_arch` is captured on every host so the gate can be written and
  tested now; `rivendell`, a FreeBSD Raspberry Pi on the same LAN, would supply the
  arm64 bytes but is not reachable from here yet (no Tailscale package for it, and its
  ssh keys are elsewhere).
- a host with **no ZFS pool at all**, which JD-006 makes a hard requirement and the
  `zfs_pool` check therefore has to report on. `capture.sh` now has the branch for it —
  it skips the per-pool probes and captures `zfs version` and `kldstat -m zfs` instead —
  but no reachable host exercises that branch.
- the write path: `zfs create` output, and PF with an actual ruleset loaded (`pfctl -s
  rules` with PF enabled and no `pf.conf` is **exit 0 and empty stdout**, which is the
  no-rules case, not an error)

Until those exist, a parser must fall through to `unknown` on anything it does not
recognise — a report that says "I could not tell" is correct where a wrong `fail` sends
someone to reboot a working machine.

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
  That replica is also #21's `409`: a dataset that exists where it should, mounted
  somewhere it should not be.
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
- **`rctl` states the `racct` remedy itself.** With racct off it exits 1 with
  `rctl: RACCT/RCTL present, but disabled; enable using kern.racct.enable=1 tunable`;
  with racct on and no rules configured it exits 0 with empty stdout. `sysctl -n
  kern.racct.enable` is the simpler primary signal, but the remedy text can be quoted
  from the tool rather than invented.
