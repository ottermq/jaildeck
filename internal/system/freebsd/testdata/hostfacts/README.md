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

## Hosts

### `vm-15.1/` — 15.1-RELEASE-p3, a libvirt VM, captured 2026-10-22

The laptop's throwaway VM (`freebsd-jails`, ssh alias `fbsd`). One pool, one jail, and
`/etc/jail.conf` laid out the way the handbook suggests: shared defaults at the top,
`exec.timeout = 120` among them, then `.include "/etc/jail.conf.d/*.conf";`.

It is a KVM guest, so its `VT-x` line describes nested virtualisation.

### `isengard/` — 15.1-RELEASE-p5, bare metal, captured 2026-10-22

The real target host: a Xeon E5620, three pools, three running jails, reached over
Tailscale. This is the authority for anything the VM cannot answer honestly — the `VT-x`
line in particular — and it disagrees with the VM in three ways that matter:

- **No `/etc/jail.conf`.** Only `/etc/jail.conf.d/{goodiesdb,ottermq,postgres}.conf`,
  each stanza self-contained, and **none of them sets `exec.timeout`**. A host can run
  jails with no shared defaults anywhere.
- **Three pools** — `backup`, `tank`, `zroot`, printed in that order. The layout is on
  `tank`.
- **`racct` is enabled**, from `kern.racct.enable=1` in `/boot/loader.conf`.

Its layout is also not identical to the VM's: `tank/jails` has `containers` and
`templates` but **no `media`**. Whatever #21 treats as a complete layout, the production
host does not have all of it today.

### `vm-15.1-induced/` — states no host was in

Four checks had no passing capture anywhere, which left their success paths written
against nothing. These were produced by changing the VM's state, capturing, and putting
it back:

```sh
kldload pf                       # -> pf_loaded_*
pfctl -e                         # -> pf_enabled_*
ifconfig bridge0 create          # -> bridge_one_group
ifconfig bridge1 create          # -> bridge_two_group, bridge_two_list, bridge_detail
# then, in reverse: ifconfig bridge1 destroy; ifconfig bridge0 destroy
#                   pfctl -d; kldunload pf
```

No script for this is committed on purpose. It mutates the host, and `kldload pf` plus
`pfctl -e` on a box that matters is not something to leave lying around next to a
read-only tool — `isengard` runs three jails on `ip4 = inherit`. Reproduce it by hand, on
a VM, or not at all. MACs are masked here as in the other sets; `bridge0`'s is generated
fresh at creation anyway.

## Where each check stands

| Check | `vm-15.1` | `isengard` | induced |
| --- | --- | --- | --- |
| `exec_timeout` | **pass** — `exec.timeout = 120` in `/etc/jail.conf` | **fail** — no `jail.conf`, no stanza sets it | |
| `racct` | **fail** — `kern.racct.enable` is `0` | **pass** — `1` | |
| `bridges` | **fail** — `vtnet0 lo0` | **fail** — `em0 em1 lo0 tailscale0` | **pass** — one bridge, then two |
| `devfs_rulesets` | **pass** — `1 2 3 4 5` | **pass** — `1 2 3 4 5` | |
| `cpu_virtualization` | **pass**, nested | **pass**, bare metal | |
| `pf` | **fail** — `pf.ko` not loaded | **fail** — `pf.ko` not loaded | **pass** — disabled, then enabled |
| `vms` | **fail** — `vmm` not loaded | **fail** — `vmm` not loaded | |
| `zfs_pool` | one pool, `zroot` | **three pools** — cannot be guessed | |
| `zfs_layout` | **pass** — `zroot/jails` → `/usr/local/jails` | **pass** on `tank`; `backup` holds a decoy | |

### Still missing

- any check in the **`unknown`** state other than a privilege error
- `VT-x: (disabled in BIOS)`, and a host with no `VT-x` line at all — neither host can
  produce these, and an AMD host would say `SVM:` instead
- a loaded `vmm`, for JD-019
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
- **`rctl` states the `racct` remedy itself.** With racct off it exits 1 with
  `rctl: RACCT/RCTL present, but disabled; enable using kern.racct.enable=1 tunable`;
  with racct on and no rules configured it exits 0 with empty stdout. `sysctl -n
  kern.racct.enable` is the simpler primary signal, but the remedy text can be quoted
  from the tool rather than invented.
