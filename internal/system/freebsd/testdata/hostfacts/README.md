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

### `isengard/` — 15.1-RELEASE-p5, bare metal, captured 2026-10-23

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
| `vm_bhyve` | **fail** — `vm` not found | **fail** | **fail** | **fail** | **fail** at each of five steps, then **pass** |
| `uefi_firmware` | **fail** — no edk2-bhyve | **fail** | **fail** | **fail** | **pass** — `BHYVE_UEFI.fd` |
| `pf` | **fail** — not loaded | **fail** | **fail** | **fail** | **pass** — disabled, then enabled |
| `vms` | **fail** — no `vmm` | **fail** | **fail** | **fail** — no `hw.vmm` oid | **loaded** — `hw.vmm` appears |
| `zfs_pool` | one pool, `zroot` | **three pools** — cannot be guessed | two pools, one named `media` | **no ZFS at all** | |
| `zfs_layout` | **pass** | **pass** on `tank`; `backup` holds a decoy | **nothing provisioned** | **n/a** | **partial**, **misplaced** |

## VM readiness (JD-019)

`vm_list` and `uefi_firmware` on every host: none has vm-bhyve or edk2-bhyve, so each
answers the shell's `sh: vm: not found` (exit 127) and no firmware directory. The replay
harness hands exit 127 with "not found" back as `system.ErrCommandNotFound`, because that
is all Jail Deck's runner ever sees of a missing command: exec fails before anything
runs, and there is no output.

`capture-vm-induced.sh`, run 2026-10-26 on isengard into `isengard-induced/` with the
prefix `vm_`, walks vm-bhyve through every state on the way to a usable install — and
then removes it, leaving `rc.conf` byte-identical. It refuses to run where vm-bhyve is
already installed or configured, since that would be an operator's.

| state | `vm list` says |
| --- | --- |
| not installed | `vm: command not found` |
| installed | `ERROR: $vm_enable is not enabled in /etc/rc.conf!`, after a WARNING |
| `vm_enable=YES`, no `vm_dir` | `ERROR: $vm_dir has not been configured or is not a valid directory` |
| `vm_dir` naming no dataset | `ERROR: unable to locate mountpoint for ZFS dataset tank/jd-vmcap` |
| the dataset exists, **not mounted** | the same line |
| mounted, before `vm init` | the empty listing, exit 0 |
| after `vm init` | the same empty listing |

**What that settled:**

- **`vm list` is the readiness probe.** It checks `vm_enable`, `vm_dir` and the datastore
  before listing anything, and names what is wrong. The check reports those words rather
  than re-deriving vm-bhyve's rules.
- **One message covers a missing dataset and an unmounted one.** On isengard the
  difference is real: `tank`'s mountpoint is `none`, so `zfs create tank/vm` makes a
  dataset vm-bhyve cannot use. The first run of the script found that out by failing
  `vm init`. The remedy says `-o mountpoint=`.
- **`vm init` is not a gate.** `vm list` and `vm datastore list` answer identically before
  and after it. It only lays out `.config`, `.img`, `.iso` and `.templates` and loads
  modules, and `rc.d/vm` runs it at every boot.
- **`sysrc -n vm_enable vm_dir` exits 1 when either is unset**, even after printing the
  one that is set, so its status cannot say which. Not used.
- vm-bhyve has a second error form, `util::err_inline`'s `  ! <message>` on stdout, read
  in its source. No `vm list` capture shows it; the adapter reads it anyway, so that it
  could never become a bare exit status.

## Listing guests (JD-035)

`capture-vm-guests-induced.sh`, run 2026-10-26 on isengard into `isengard-induced/` with
the prefix `vm_guests_`, needs vm-bhyve already configured and answering. It makes two
guests that boot no OS — an empty disk and UEFI are a running bhyve all the same —
`jd-cap-running`, started on a switch of its own with no physical member, and
`jd-cap-stopped`, with no network; captures `vm list`, `vm list -v` and `vm info` for
each; adds the running one to `vm_list` and captures again; and then destroys both, its
templates and the switch, leaving `rc.conf` byte-identical. It refuses to run where any of
those names exist, or where `vm_list` is set, since that would be an operator's.

**What that settled:**

- **`vm list` cannot be split on whitespace, nor read by column offset.** `AUTO`
  (`Yes [1]`) and `STATE` (`Running (16783)`) hold spaces, and `column -t` re-pads every
  row to the widest value: marking one guest for boot moved `STATE` three characters
  right. They are the last two columns, so a row is read from both ends.
- **FreeBSD's `column -t` merges an empty field into the next one** — tried directly,
  since no guest had an empty `MEMORY`. The parser leaves `cpu` and `memory` unknown for a
  row with fewer than five middle columns, rather than guessing which is missing.
- **The `pid` in `Running (pid)` is bhyve's,** not that of `vm-run`, the shell
  supervisor that is its parent (`vm_guests_vm_run`).
- **`vm info` with no guests exits 1 and prints nothing** (`vm_guests_none_info`), and
  for an unknown name it is vm-bhyve's `ERROR: unable to locate virtual machine`, exit 1.
- **A stopped guest's `vm info` has no `memory-resident` and no `console-ports`.** Both
  come from the bhyve instance, which a stopped guest does not have.
- `-v` adds `%CPU`, `RSZ` and `UPTIME`. Not used: the parser accepts only the eight-column
  heading it was written against.

The other three states — `Bootloader (pid)`, `Suspended` and `Locked (host)` — were not
induced. They are read from vm-bhyve's source (`lib/vm-core`, `lib/vm-run`) and tested
from rows written to match it.

## Starting and stopping a guest (JD-036)

`capture-vm-lifecycle-induced.sh`, run 2026-10-26 on isengard into `isengard-induced/` with
the prefix `vm_life_`, makes one guest that boots no OS, `jd-cap-life`, and starts, stops and
powers it off. Before and after each step it records the guest's state once a second
(`vm_life_after_*`). It then destroys the guest and its template, leaving `rc.conf`
byte-identical. It refuses to run where a `jd-cap-*` guest or its template exists.

**What that settled:**

- **vm-run and bhyve stay in `vm start`'s caller's process group and session**
  (`vm_life_process_tree`). vm start backgrounds vm-run without job control, so a runner
  that kills a command's group on a timeout would kill the guest. Jail Deck detaches it.
- **vm start's pipe closes in a second.** vm-run's output is `/dev/null`, so nothing
  holds the caller's stdout (`vm_life_start_piped`).
- **vm start returns before the guest is up:** `Stopped` on the first look, `Running` a
  second later (`vm_life_after_start`).
- **Refusals are `  ! <reason>` on stdout with exit 1:** a guest already running, a name
  that is not a guest.
- **`vm stop` exits 0 for a guest that is not running**, with a WARNING on stderr.
  `vm poweroff -f` of one is exit 1 with an ERROR. On a running guest it is silent and
  exit 0.
- **A guest with no OS answers the power button at once,** even in its first second
  (`vm_life_after_stop_early`). JD-019's guest that ignored it had an OS still booting.
  None could be made without an image, so the poweroff fallback is tested against a
  scripted vm-bhyve. *JD-037's capture, below, then made one.*

## VM image templates (JD-037)

Two scripts, both run 2026-10-26 on isengard into `isengard-induced/`.

`capture-vm-images-induced.sh`, prefix `vm_img_`, **downloads Debian 13's genericcloud
image (217 MiB)**, checks it against Debian's `SHA512SUMS`, writes its `disk.raw` into a
zvol under `tank/vm/.images` and snapshots it. It then makes a guest, `jd-cap-img`, whose
`disk0` is a clone of that snapshot grown to 8G, with a cloud-init seed that powers it off
after its first boot, and stops it three more times. It also makes one clone in GEOM's
default mode, to record what that does. Everything is destroyed afterwards, the datasets
are as they were and `rc.conf` is byte-identical. It refuses to run where `.images`, a
`jd-cap-*` guest, its template or its download directory exists. It was run twice; the
first run, without `volmode=dev`, is the one that found the GEOM hold, and these are the
second's captures.

`capture-vm-image-list-induced.sh`, prefix `vm_imgls_`, downloads nothing. It makes
`.images` and two 64 MiB zvols standing in for images, one sealed and cloned for a guest's
disk and one never sealed, and reads them with **the exact commands the adapter runs**, so
the adapter's tests replay them by command line.

**What that settled:**

- **A clone grown past its image, in GEOM's default `volmode`, is held by the kernel.**
  `kern.geom.part.auto_resize` moves the backup GPT header and keeps the disk open for
  exclusive write until `gpart commit` or `gpart undo` (`vm_img_geom_clone_held`: the
  consumer is `r1w1e1`, and dmesg says so). `zfs destroy` is then "dataset is busy"
  (`vm_img_geom_destroy_held`), and a guest on it found nothing to boot. `volmode=dev`
  keeps GEOM out (`vm_img_gpart` has no geom at all).
- **tar can write the disk straight into the zvol.** `vm_img_write` is the one `dd` pipeline
  the capture used; Jail Deck runs `tar -xOf` with its output pointed at the device, which
  was checked byte for byte with a synthetic archive, not captured here.
- **`zfs clone -o volsize=` grows the clone in one step** (`vm_img_clone`, `vm_img_clone_props`:
  56 KiB used), and the guest grows its own partition into it on first boot: the GPT
  backup header moves from sector 6291455 to 16777215 (`vm_img_clone_gpt_before`,
  `vm_img_clone_gpt_after`). `vm_img_boot1_console` is the serial console's milestones,
  filtered from 210 KB of escape sequences.
- **`vm create -C` writes the seed at the first start**, not at create time
  (`vm_img_guest_info` cannot find it yet; `vm_img_boot1_log` says it was made).
- **A Debian guest booting with no network ignores the power button** at 10 s and at 60 s,
  for the whole 70 s watched (`vm_img_after_stop_booting_10s`, `vm_img_after_stop_booted`
  — the second is misnamed: at 60 s it was still booting, which took 141 s). At 3 s, in the
  firmware or GRUB, it went off at once (`vm_img_after_stop_booting_3s`).
- **`Locked (isengard)` is a state vm-bhyve shows for a second** while a guest powers
  itself off, with bhyve gone and vm-run still exiting (`vm_img_boot1`, at 136 s).
- **zfs refuses to destroy an image with a clone** (`vm_img_destroy_snapshot_in_use`,
  `vm_imgls_destroy_in_use`); `vm destroy` of the guest takes the clone and frees it
  (`vm_img_after_guest_destroy`).

## Creating a guest from an image (JD-038)

`capture-vm-create-induced.sh`, run 2026-10-26 on isengard into `isengard-induced/` with the
prefix `vm_mk_`, uses the image `tank/vm/.images/debian-13`, which it needs to exist and leaves
alone. It makes a throwaway switch, `jd-capsw`, with the host at 10.77.0.1/24 on it, and one
guest, `jd-cap-made`, the way Jail Deck does: `vm create` from a UEFI template with no NIC and a
cloud-init seed holding a throwaway SSH key and 10.77.0.10/24, `vm add` of a NIC on the switch,
and disk0 replaced by a `volmode=dev` clone of the image at 8G. It boots it, reaches it over
SSH from the host, stops it, boots it again, and destroys it, the switch and the template.
Datasets were as before, and `system.conf` and `rc.conf` byte-identical. The key pair lived in
a directory the script removed; only the public key is in the captures, in the seed.

**What that settled:**

- **The NIC `vm add` makes is network0, with the MAC `vm create` generated**
  (`vm_mk_conf_created`, `vm_mk_conf_networked`), which is the MAC the seed's network-config
  matches (`vm_mk_seed_files`). Inside, `eth0` had the seed's address and route
  (`vm_mk_inside`), and the root filesystem had grown into the 8G clone.
- **`vm add -d network` takes a switch that does not exist** (`vm_mk_add_network_nosuch`,
  exit 0) and the guest then runs with a tap on no bridge (`vm_mk_info_running`, network 1).
- **SSH answered 18 s after `vm start`** (`vm_mk_reach`), and 13 s on a second boot
  (`vm_mk_reach_again`). A booted guest on a network obeyed the power button in 3 seconds
  (`vm_mk_after_stop_booted`).
- **vm-bhyve's refusals:** a name taken (`vm_mk_create_taken`), a netconfig key it does not know
  (`vm_mk_create_badnet`), destroying a running guest (`vm_mk_destroy_running`: exit 1,
  "appears to be running on isengard (locked)").
- **`vm destroy` takes the clone and frees the image** (`vm_mk_after_destroy`).

## Tenant networks (JD-043)

`capture-tenant-network-induced.sh`, run 2026-10-28 on isengard into `isengard-induced/` with
the prefix `ten_`, **changes the host's networking and loads PF**. It refuses where PF is
already loaded, and a watchdog unloads PF after ten minutes. It makes two throwaway tenants,
each a bridge with the host on `.1` and a vm-bhyve manual switch on it:
- **A:** `jdt-a`, `10.91.1.0/24`;
- **B:** `jdt-b`, `10.91.2.0/24`;
- on each, a Debian guest (`.10`, from the kept image) and a VNET jail (`.20`). The jail roots
  are clones under `<pool>/jdcap-tenant`, outside what zrepl copies.

It probes in four layouts: no PF; PF rules with the block first; PF rules with the pass first;
and the same members moved onto one VLAN-filtering bridge. Then it times 256 bridges. Only
the throwaway interfaces are recorded.

Everything was put back: forwarding as it was (`1`), PF unloaded, the interfaces as before, and
`rc.conf` byte-identical. `system.conf` kept the two `bridge_jdt-*` lines `vm switch destroy`
leaves for a manual switch; they were removed by hand, and the finding went into the adapter.

`net_tenant_bridge` and `net_bridge_group` are one `jdn` bridge made as the adapter makes one,
with an epair on it, read back with `ifconfig -f inet:cidr`.

**What that settled:**

- **The host routes one tenant into another** once forwarding is on: guest to guest, guest to
  the other tenant's jail, and SSH to the host through the other tenant's gateway all reached
  (`ten_nopf_*`, `0` is reached). Isolation is PF's.
- **One tenant's two rules, the pass to its own gateway first, keep it apart** (`ten_pfp_*`):
  the other tenant, its gateway and the host's SSH there were blocked, the own gateway reached,
  and the internet reached through NAT. **With the block first** (`ten_pf_*`), the host's own
  SSH to the guests failed, so those probes are empty: the block of `self` also caught the
  replies.
- **VLANs on one bridge work, and route like separate bridges do** (`ten_vlan_*`). The gateways
  work as `vlan(4)` on the bridge (`ten_vlan_gateways`), and a guest reset keeps its tap and
  its VLAN (`ten_vlan_after_reset`).
- **256 bridges: created in 1 s, destroyed in 3 s** (`ten_scale_*`).
- **Pings from inside a VNET jail failed** throughout, even within its own tenant, while TCP
  from the same jail reached (`*_ja_tcp_a1`). That's a limit of the probe, not of the network;
  the jail's reach is read from TCP.

## A shell in a jail on a PTY (JD-042)

`capture-jail-console-induced.sh`, run 2026-10-28 on isengard, writes `jpty_*` files; they are
kept in `isengard-induced/` as `jail_con_*`. It drives the installed Jail Deck through its
API, with the token passed to curl in a private config file. It creates one throwaway jail,
`jd-cap-pty`, from the 15.1-RELEASE template with networking disabled, and destroys it again.
**`ptyprobe`** (in `ptyprobe/`, cross-built with `GOOS=freebsd`) starts the shell as the console
will: `jexec -l <jail> /bin/sh -l` on a PTY through `creack/pty`, with `TERM` and one planted
variable in its environment. The host's other jails were left alone.

The destroy was first refused: zrepl on isengard had snapshotted the jail in the meantime and
replicated it to the backup pool. The snapshot went through the API; the backup copy was
reported to the owner. In the first run, the MANIFEST recorded the token inside the curl
command lines. It was blanked to `<token>` before anything was copied, and the script was
changed so that no command line carries it.

**What that settled:**

- **`jexec -l` passes `TERM` and nothing else of the caller's environment.** The planted
  variable didn't reach the shell; the login shell set its own `PATH`, `HOME` and `SHELL`
  (`jail_con_basic`).
- **A resize reaches the shell:** `stty size` answered `24 80`, then `40 120` after `Setsize`.
  The shell's terminal is `/dev/pts/1`.
- **A shell that exits ends the read at once with EOF,** and `jexec` exits 0.
- **A jail stopped under an open shell takes 10 seconds to stop**, against under a second with
  none open (`jail_con_stop_alone`, `jail_con_stop_jail`). The shell is then killed, and the
  read ends with EOF (`jail_con_stop`).
- **Closing a blocking PTY master does nothing useful.** The blocked read never returned,
  `cmd.Wait` hung, and the shell and the `sleep 600` it was running stayed alive in the jail
  until the probe was killed (`jail_con_hangup`). **On a non-blocking duplicate of the master,
  closing it ended the read at once,** `jexec` got `SIGHUP`, and nothing was left in the jail
  but cron (`jail_con_hangup_nb`, `jail_con_after_hangup_nb`).

## A guest's serial console (JD-042)

`capture-vm-console-induced.sh`, run 2026-10-28 on isengard into `isengard-induced/` with the
prefix `vm_con_`, needs the path of **`conprobe`** as its argument. `conprobe` is the small Go
program in `conprobe/`, cross-built with `GOOS=freebsd`. It opens the console device with the
calls Jail Deck will make: `O_NONBLOCK`, x/term's `MakeRaw` and `TIOCEXCL`. The script makes
one throwaway guest, `jd-cap-con`, as Jail Deck makes one, from the kept Debian image with a
seed and no NIC, and waits three minutes for its getty: with no NIC, Debian's boot first waits
out `systemd-networkd-wait-online`. Only returns are ever typed. The guest and template were
destroyed again, datasets were as before, and `system.conf` and `rc.conf` byte-identical.
Another guest on the host was left alone.

The first subject was the UEFI firmware with nothing to boot. It was dropped, because its setup
screen redraws itself at about 200 KB/s and hardly reads its input, which hid everything else.
Its runs weren't kept.

**What that settled:**

- **The host's end opens in a terminal's normal mode** (`vm_con_raw` prints it after
  `MakeRaw`; `vm_con_cooked` before). **Made raw at once, a return gets the login prompt**
  (`vm_con_raw`). **Left as it is, the guest's output is echoed back to it as input**: 17,105
  bytes of newlines in 9 seconds (`vm_con_cooked`), and the next raw session found the echoed
  input typed at the login prompt, `Login incorrect` (`vm_con_raw_again`).
- **`TIOCEXCL` doesn't stop a second opener that is root** (`vm_con_excl`), and `cu` connected
  meanwhile (`vm_con_excl_cu`).
- **A guest reset doesn't end the session.** The same descriptor wrote, and read the new
  boot (`vm_con_reboot`, `vm_con_after_reset`).
- **A guest powered off gives a waiting reader nothing for 60 seconds**: no end of file, and
  a write still succeeds (`vm_con_stop`). The device is gone once closed
  (`vm_con_devices_after`), and **opening it again creates a new, unconnected pair**
  (`vm_con_open_after`). The devices don't exist before the guest first starts
  (`vm_con_devices_stopped`).

## Pinning a guest's vCPUs (JD-041)

`capture-vm-pin-induced.sh`, run 2026-10-28 on isengard into `isengard-induced/` with the prefix
`vm_pin_`, makes one throwaway guest, `jd-cap-pin`, from a template of its own: UEFI, 2 vCPUs,
256M and an empty 1G disk, so the firmware runs and finds nothing to boot, which is enough to
have vCPU threads. It writes `bhyve_options` into the guest's configuration with `sysrc -f`, as
vm-bhyve's `config::set` does, starts the guest, and reads each vCPU thread's cpuset with
`cpuset -g -t`, four ways. The guest and its template were destroyed again; datasets were as
before, and `system.conf` and `rc.conf` byte-identical. Another guest on the host was left alone.

**What that settled:**

- **Unpinned, both vCPU threads may run on all 16 CPUs** (`vm_pin_unpinned_cpusets`), like the
  host's root set (`vm_pin_rootset`).
- **`-p 0:2 -p 1:3` pins vCPU 0 to CPU 2 and vCPU 1 to CPU 3** (`vm_pin_pinned_cpusets`).
  vm-bhyve passes the options to bhyve as they are (`vm_pin_pinned_log`).
- **A pin of one vCPU leaves the other free** (`vm_pin_onepin_cpusets`).
- **A host CPU that does not exist makes bhyve exit with status 134, and `vm start` exits 0**
  (`vm_pin_badcpu_start`, `vm_pin_badcpu_log`, `vm_pin_badcpu_running`): why the service checks
  `hw.ncpu` (`vm_pin_ncpu`) first.
- **`vm create` writes `network0_mac` even with no NIC** (`vm_pin_conf`), so a NIC is counted by
  `network<N>_type` or `_switch`, not by its MAC.
- **`sysrc -x -f` takes the setting away again** (`vm_pin_conf_unset`).

## The default VM network (JD-040)

`capture-vm-network-induced.sh`, run 2026-10-28 on isengard into `isengard-induced/` with the
prefix `vm_net_`, **changes the host's networking and loads PF**, so it refuses to run where PF
is already loaded and turns PF off again from a watchdog after six minutes whatever happens. It
uses the image `tank/vm/.images/debian-13` and leaves it alone. It makes a throwaway switch,
`jd-netsw` (10.81.0.0/24, the host on .1), and two guests on it, `jd-net-a` (.10) and
`jd-net-b` (.11), and has `jd-net-a` reach the other guest (ICMP and TCP), the host, the
internet and DNS three ways: the switch shared, the switch private, and private with one NAT
rule in the `jaildeck/vmnet` anchor and forwarding on. PF was loaded with a ruleset that passes
everything **without keeping state** and includes only the `jaildeck/*` anchors. Everything was
put back: PF unloaded, forwarding as it was, the guests, the switch and the template destroyed.
Run detached under `daemon(8)`, because enabling PF can drop the SSH session that started it.

**Sanitized:** the host's own switches are left out of `vm_net_switch_list_private`, and a probe
of the LAN's gateway was removed, as the script no longer makes it.

**What that settled:**

- **A private switch keeps guests apart and leaves the host reachable** (`vm_net_private_a_to_b`
  and `_tcp_b` exit 1, `vm_net_private_a_to_host` exit 0); on the shared switch the same guests
  reach each other (`vm_net_shared_a_to_b`, `_tcp_b`). vm-bhyve marks each tap `PRIVATE` as the
  guest starts (`vm_net_private_bridge`).
- **NAT through the anchor, with forwarding on, reaches the internet and DNS** and still keeps the
  guests apart (`vm_net_nat_a_to_internet`, `vm_net_nat_a_dns`, `vm_net_nat_a_to_b`). Without
  NAT, the private switch reaches nothing beyond the host (`vm_net_private_a_to_internet`).
- **pfctl prints the anchor's rule in its own form**, `nat on em0 inet from 10.81.0.0/24 to any
  -> (em0) round-robin` (`vm_net_pf_anchor`), and the main ruleset's include as `nat-anchor
  "jaildeck/*" all` (`vm_net_pf_anchors`): what the service compares against.

## The addresses in use on a network (JD-047)

`capture-network-addresses-induced.sh`, run 2026-10-30 on isengard into `isengard-induced/` with
the prefix `nad_`, **changes the host's networking**. It makes a throwaway bridge, `jdcap0`
(10.79.0.0/24, the host on .1, no physical member), and a throwaway VNET jail, `jd-cap-arp`
(`path=/`, no dataset), on a private port. The jail gives itself .5 and says nothing, pings the
gateway, then gives itself .77 by hand and pings from it. Both were destroyed again, and the
host's epairs were as many as before. The host ran with `net.link.bridge.pfil_member=1`
(`nad_pfil_member`).

**What that settled:**

- **An address that has said nothing is not in the bridge's ARP table** (`nad_arp_quiet`: the
  gateway alone, `permanent`). So the table can't be where Jail Deck learns which addresses are
  taken. The jails' stanzas and the guests' seeds are.
- **What has talked is, with an expiry** (`nad_arp_talked`: `expires in 1200 seconds`, which is
  `net.link.ether.inet.max_age`, `nad_max_age`), **an address set by hand inside the jail
  included** (`nad_arp_selfset`, .77). So the table adds what no configuration says, for as long
  as it is recent.
- **The host's own address is `permanent`**, and is the only such entry.
- **A bridge that is not there is exit 1**, `arp: interface jdnope0 does not exist`
  (`nad_arp_nosuch`).

## What a guest's directory holds (JD-048)

`capture-vm-configuration-induced.sh`, run 2026-10-30 on isengard into `isengard-induced/` with
the prefix `vcf_`, **changes the host**. It makes two throwaway guests on an empty 1G sparse
disk, with no image and no switch. `jd-cap-cfgu` is made with `vm create -u`, from a user-data
file as Jail Deck writes one for a console password, its hash a placeholder in bcrypt's shape.
`jd-cap-cfgk` is made with `vm create -k`. It starts the first one once, powers it off, and
destroys both and the template again; `system.conf` was as before.

**What that settled:**

- **The directory and everything vm-bhyve writes in it are `root:wheel`, files `0644` and
  directories `0755`** (`vcf_dir_u`, `vcf_dir_k`): `<name>.conf`, `vm-bhyve.log`,
  `.cloud-init/meta-data`, `.cloud-init/network-config`, and `user-data` made from `-k`.
- **A `user-data` given with `-u` keeps its own mode**: Jail Deck writes it `0600`, and the
  seed's copy is `0600` (`vcf_user_data_scratch`, `vcf_dir_u`). An unprivileged account is
  refused it (`vcf_read_as_user`).
- **The first start adds `seed.iso`, `console` and `run.lock`**, and the configuration
  gains `disk1` for the ISO (`vcf_dir_u_started`, `vcf_conf_u`). `seed.iso` stays after the
  guest stops (`vcf_dir_u_stopped`).
- **`rctl jail:<name>` for a jail with no rules prints nothing and exits 0** (`vcf_rctl_none`).
## A guest's directory, made private (#152)

`capture-vm-private-dir-induced.sh`, run 2026-10-30 on isengard into `isengard-induced/` with the
prefix `vpd_`, **changes the host**, and runs as a user that is not root, because what that user
can read is the point. It makes a throwaway guest, `jd-cap-priv`, on an empty 1G sparse disk
with no image and no switch, from a user-data file as Jail Deck writes one for a console
password (`vm create -u`), its hash a placeholder in bcrypt's shape. It starts it and powers it
off, makes its directory 0700, starts and powers it off twice more, and destroys it and the
template again; `system.conf` was as before.

**What that settled:**

- **As `vm create` and the first start leave it, the directory is 0755 and `seed.iso` 0644**
  (`vpd_mode_as_made`). A plain account reads the configuration and `seed.iso`, and the seed's
  user-data with it, though `user-data` itself is 0600 (`vpd_read_as_made`).
- **Made 0700, nothing in it can be read by that account**, stopped or running
  (`vpd_read_private`, `vpd_read_running`).
- **vm-bhyve keeps the mode**: 0700 while running and after two more starts and stops
  (`vpd_mode_running`, `vpd_mode_after_restarts`), and `vm start`, `vm list` and `vm destroy`
  work as before (`vpd_list_running`, `vpd_list_restarted`, `vpd_destroy`).

## Disk health (#83)

Captured 2026-10-25, on `isengard` and `rohan` only. `rivendell` and the VM were not
captured: the Pi has no SMART-reporting storage worth the trip, and the VM's virtio
disks answer nothing a parser could learn from.

**`smartctl` is a package, not base** — `sysutils/smartmontools`, at
`/usr/local/sbin/smartctl`. A host without it is a state the report has to describe, so
`smartctl_version` is captured even when the binary is missing.

**The scan list is not a list of disks**, and `isengard` proves it three ways:

| device | `--scan` says | exit | what it is |
| --- | --- | --- | --- |
| `ada0`–`ada3` | `-d atacam` | `0`, and `32` for `ada3` | the four spinning disks |
| `nvme0` | `-d nvme` | `0` | the NVMe, answering properly |
| `nda0` | `-d scsi` | **`2`** | **the same NVMe**, listed a second time under another driver, and it cannot be opened that way |
| `cd0` | `-d atacam` | **`4`** | a DVD drive |
| `ses0` | `-d atacam` | **`2`** | an enclosure |

So the duplicate resolves itself: the device that answers is the device to report.

**But the device that answers is not the device ZFS names.** `zroot` stands on `nda0`,
and `smartctl` refuses `/dev/nda0` outright:

```
Smartctl open device: /dev/nda0 failed: NVMe controller controller/namespace ids
must begin with '/dev/nvme'
```

It will only answer as `/dev/nvme0` — including under `-d nvme`, which was measured
rather than assumed. So the one disk that holds the root pool is the one disk nothing
joins to a pool by name. Guessing that `nvmeN` is `ndaN` would be a convention; the
serial is a fact, and `geom disk list` prints it beside the kernel's own name:

| source | name | serial |
| --- | --- | --- |
| `geom disk list` | `nda0` | `P300MDCB…` |
| `smartctl /dev/nvme0` | `/dev/nvme0` | `P300MDCB…` |

`geom_disk_list` is captured for that join. It is base system and needs no privilege,
which makes it both cheaper and more authoritative than the scan.

**The join is by name first and by serial second, and both halves are load-bearing** —
one host proves each:

- **by serial**, for `isengard`'s NVMe above, where nothing matches by name;
- **by name**, for `rohan`'s `da0`, a bare drive in a USB enclosure where the two
  sources report *different* serials — `geom` gives the enclosure's (`2HC015KJ`) and
  `smartctl` the drive's own (`5VP6Q01R`). A serial-first rule would fail to place a
  disk whose name already matched perfectly.

Neither host has a disk whose smartctl serial is another disk's `geom` ident, so the
captures alone do not say which lookup should win; the unit test that fixes the order
says it is constructed.

`zpool_list_vdevs` and `glabel_status` carry the rest of the join. A pool names its
members however it was created: `isengard`'s `backup` says `gpt/backup1` and its `tank`
says `ada0p4`, so resolving a label and stripping a partition are both ordinary paths
rather than one being a special case.

`rohan` adds the two shapes `isengard` has none of: an **SSD** (`rotation_rate: 0`,
where `isengard`'s disks report 5400 and 7200) and a **`-d sat`** device at `/dev/da0`,
which is how a USB bridge presents an ATA disk.

**`isengard/smartctl_ada3` is the specimen this whole feature exists for.** It is
simultaneously `"passed": true` on the drive's own assessment and `"when_failed": "now"`
on attribute 184, with zero reallocated sectors and an extended self-test that completed
without error at 19,149 hours. No single number describes that drive, which is the
argument against inventing one.

Two more states carry their own exit status and are captured: a device that is not there
(`smartctl_absent`, exit `1`) and a caller who is not root (`smartctl_unpriv`, exit `1`
with `Permission denied` on stderr).

**Exit status is information here, not failure.** `ada3` exits `32` — bit 5, "an
attribute has been below threshold in the past" — on a perfectly successful read. A
parser that treats non-zero as an error reports nothing about the one disk that matters.

Serial numbers and the unit-unique half of each WWN are masked, on the same reasoning as
the MAC addresses in `dmesg`: the repository is public, and nothing a parser reads is
affected.

The serials have one constraint the MACs did not, and it is the reason `capture.sh`
masks them the way it does. Because the report **joins** `smartctl`'s `serial_number` to
`geom`'s `ident`, the two have to mask to the *same* placeholder — and two disks have to
mask to *different* ones. Blanking each serial to a run of `X` would do neither:
`isengard`'s `ada2` and `ada3` both have eight-character serials and would collide, and a
fixture asserting a join that could never happen is worse than no fixture. So each
distinct serial gets a distinct placeholder of its own length (`S01XXXXX`, `S02XXXXX`),
substituted across every captured file at once.

Two bugs in that masking were caught by looking at the bytes before committing them:
an `"id":` regex that matched SMART attribute ids and turned attribute `184` into `0`,
and a collected "serial" of a single space — the DVD drive's empty `ident` — which
rewrote every space in every file. The filter now takes only idents of four or more
characters with no whitespace.

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

## Firewall visibility (#16)

`capture-pf-induced.sh`, run 2026-10-25 on the VM (15.1-RELEASE-p3), into
`vm-15.1-induced/` with the prefixes `pf_off_`, `pf_ruleset_` and `pflog_`. It loads `pf`
and `pflog`, loads a test ruleset **from a temporary file** — `/etc/pf.conf` is never read,
written or created (JD-020) — enables PF, sends a few packets, captures, and restores. It
refuses to run on a host where PF is already loaded, because flushing someone's policy to
load ours is exactly what this project never does.

The VM, not isengard: the output's shape does not depend on hardware, and a firewall
experiment on the deployment target risks the SSH session running it. **Neither isengard
nor rohan has PF loaded**, so on the real hosts this feature mostly has to say so clearly.

The test ruleset carries every shape the view has to render: a table with addresses and
one without, labels, a logged rule, a NAT rule, a wildcard anchor with rules loaded into
it, and an inline anchor. Nothing it blocks is a real host (RFC 5737 ranges).

**What the bytes said that the plan did not:**

- **`pfctl -s rules` does not show NAT.** The `nat` rule is in `pf_ruleset_nat`
  (`-s nat`) and nowhere in `pf_ruleset_rules`. JD-020's command list omits `-s nat`, so a
  view built on it would hide every translation rule.
- **An anchor's rules are not in the main listing.** `-s rules` shows
  `anchor "jaildeck/*" all` and stops; the contents need `-a <anchor> -s rules` per anchor.
  `-s Anchors` lists all of them flat, wildcard parents and inline anchors included.
- **A nonexistent anchor exits 0** (`pf_ruleset_anchor_absent`). The only sign of it is
  `pfctl: Anchor does not exist.` on stderr. A nonexistent table exits **255**
  (`pf_ruleset_table_absent`). Neither exit status is a reliable answer on its own.
- **`-v` prints `No ALTQ support in kernel` to stderr on every call, with exit 0.** A
  reader that treats stderr as failure fails on every host without ALTQ.
- **`-s Tables -vv` is wrong about tables.** `pf_ruleset_tables_vv` reports
  `Addresses: 1` for a table `pf_ruleset_table_show` lists two entries in, and
  `References: Rules: 0` for a table a rule uses — while the same rule under
  `-s rules -vv` reads `<jaildeck_test_blocked:2>`. Reproduced by hand with three entries:
  still `Addresses: 1`. Its flags (`-pa-r--` from `-s Tables -v`) are consistent with
  everything else; its statistics are not used.
- **pfctl's "Disabled for" is time since boot when PF has never been enabled.**
  `pf_ruleset_disabled_info` says `Disabled for 0 days 00:10:19` for a PF loaded seconds
  before; checked by hand, the figure equals the host's uptime. The clock restarts at every
  `pfctl -e` and `pfctl -d`, so it is "since the last switch, or since boot".
- **PF is last-match-wins, and it emptied the first capture's pflog.** A logged rule
  without `quick`, followed by `pass out all`, never fires: the later rule matches too and
  wins. The script's logged rules are `quick` for that reason.
- **`log` records the packet that creates a state, not the traffic.** Six packets matched
  the logged ICMP rule; one line was logged. `log (all)` would log every packet.
- **pflog lags.** `pflogd` buffers and flushes every 60 seconds by default
  (`pflogd(8)`, `-d delay`), or on `SIGALRM`. `pflog_tcpdump_unflushed` is the file read
  straight after the packets — empty — and `pflog_tcpdump` the same file after a flush.
- **`-ttt` prints the time since the previous packet**, so the first line of any read is
  `00:00:00`. `-tttt` is the flag that gives a date and time.
- **pflog names a rule by number and an anchor by its last component.** The ICMP line says
  `rule 2/0` — the `@2` that only `-s rules -vv` prints — and the line from inside
  `jaildeck/web` says `rule 5.web.1/0`: main rule 5, anchor `web`, rule 1 inside it. Two
  anchors named `a/web` and `b/web` are indistinguishable in the log.
- **`/var/log/pflog` is rotated at 1000 KB** by `/etc/newsyslog.conf.d/pf.conf` on all
  three hosts — not by `newsyslog.conf` itself, which is where a first look finds
  nothing.

## Storage visibility (#2, #3)

The Storage Visibility milestone fixes the batched read as
`zfs list -H -o name,used,avail,refer,mountpoint <paths…>`, and #2 wants a jail's child
datasets and its **user properties** — the ones whose name contains a colon — from one
call rather than one per jail. These captures are what that gets parsed against.

Per pool, in the same loop as the layout captures:

| | |
| --- | --- |
| `zfs_list_space_r_<pool>` | the milestone's columns, **human-readable** |
| `zfs_list_space_rp_<pool>` | the identical query with `-p` |
| `zfs_list_mounted_r_<pool>` | `mounted`, `canmount` and `mountpoint` — three different questions |
| `zfs_get_local_r_<pool>` | every locally-set property on every dataset, one call (`-s local`) |

Per dataset, on whichever pool has the jails layout — `PROBE_DATASET` records which, and it
is deliberately **not** the first pool printed, for the reason JD-021 gives: `zfs_list_depth1`
and `_depth0`, `zfs_list_space_mixed`, `zfs_get_all` and `zfs_get_local`, `zfs_get_used` and
`_used_p`, `zfs_get_unset_userprop`, `zfs_get_bad_property`, `zfs_get_absent_dataset`,
`zfs_list_snapshots` and `_p`, `zfs_list_snapshots_absent`, and `zfs_list_type_all`.

**What these corrected, before a parser existed:**

- **The size columns are human-readable.** `zfs_list_space_r_tank` says `2.20G`, `897G`,
  `96K`; `zfs_list_space_rp_tank` says `2360832000`, `963201544192`, `98304`. Same query.
  A parser wanting numbers needs `-p`, and one wanting what an operator sees must not.
- **`zfs list -d 0 -t snapshot <dataset>` returns nothing, at exit 0.** A dataset's
  snapshots are its depth-**1** children, so depth 0 filters out the filesystem itself and
  leaves an empty list that reads exactly like "no snapshots". The captures use `-d 1`.
- **`creation` is a locale-formatted date**, and `Wed Oct 21  0:44 2026` carries a *double*
  space before a single-digit hour. The fields are tab-separated, so splitting the line on
  whitespace loses. `-p` gives a Unix timestamp instead.
- **Three different failure shapes, three different exit codes.** An unset *user* property
  is not an error: `zfs_get_unset_userprop` is exit 0 and one row,
  `tank/jails  otter:instance  -  -`. An invalid *native* property is exit **2**. A dataset
  that does not exist is exit 1. An adapter that treats "no such property" as one condition
  gets two of the three wrong.
- **`zfs_get_bad_property.stderr` is 7 KB** because `zfs get` answers an invalid property
  with its entire usage text. Kept deliberately: it is also the complete native-property
  vocabulary of this ZFS version, which is what #3's property allowlist has to be built
  from.
- **Real user properties exist here, in both conventions.** `zrepl:placeholder` on
  `backup/isengard` and `backup/isengard/tank`, and the reverse-DNS `com.sun:auto-snapshot`
  on `rohan`'s `zroot/srv/builds`. So #2's feature has captured examples of a property that
  **is** set, not only of one that is not.
- **`tank`'s jails are tagged, deliberately and by hand.** The 2026-10-23 capture was taken
  after setting, on isengard:

  ```
  zfs set otter:instance=mq-01 tank/jails/containers/ottermq
  zfs set otter:tier=broker    tank/jails/containers/ottermq
  zfs set otter:instance=db-01 tank/jails/containers/postgres
  zfs set atime=off            tank/jails/containers/ottermq
  ```

  This is the only time anything in `hostfacts/` was captured from a state arranged on
  purpose on a real host rather than found on one, and the arrangement is three-sided:
  `ottermq` carries two tags, `postgres` one, and `goodiesdb` **none**, so the absent case
  comes from the same host as the present one. Before this, every container on every host
  had no local properties at all, which left the headline feature of #2 — find your jails
  by `otter:instance` — with no evidence.

  The `atime=off` is the subtle half and it is not decoration. `tank` already sets
  `atime=off`, so the container inherited it and the value does not change; what changes is
  the **source**, from `inherited from tank` to `local`, which puts a *native* locally-set
  property on a jail's own dataset. Without one, deleting the colon filter that separates
  user properties from native ones changes nothing any jail ever sees, and the test that
  claims to check it passes either way. It was found exactly that way: by deleting the
  filter and watching the suite stay green.

  Reverting is `zfs inherit <property> <dataset>` for each of the four.
- **`zfs_list_snapshots_txg` is the one capture that churns.** It is scoped to a single jail
  (`PROBE_JAIL`, which prefers one that has a child dataset) rather than depth-limited, because
  the parent/child pair is the format it exists to show: one `zfs snapshot -r` gives both the
  same `createtxg`, and that is the evidence that a recursive snapshot really is one point in
  time (JD-024). One jail on isengard is 39 lines, 35 of them hourly `zrepl` snapshots, so a
  re-capture moves it. That is the price of keeping the pair; recursive over the whole layout
  would be 276 lines.
- **`zfs_get_clones_none` and `zfs_holds_none`** are what "nothing depends on this snapshot"
  looks like: a dash and an empty stdout, both at exit 0. Neither is an error, which is the
  point — the no-blockers path has to recognise them as answers rather than failures. The
  blocked shapes have no capture: arranging a hold and a clone on a real host to photograph
  them was not worth leaving behind, and the service tests construct them and say so.
- **Snapshot captures are depth-limited on purpose.** Recursive over a pool, isengard
  returns 276 lines of which 256 are hourly `zrepl` snapshots — a fixture that teaches a
  parser nothing more and rewrites itself every hour. `vm-15.1` and `rohan` supply the
  empty-list shape (exit 0, no output) that isengard cannot.

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
- **`kern.cp_time` counts in `stathz`, not `hz`.** `kern_clockrate` reads
  `{ hz = 1000, tick = 1000, profhz = 8128, stathz = 127 }` on both bare-metal hosts, and
  over two seconds the five `kern.cp_time` counters advanced 3995 ticks across 16 CPUs —
  125 per CPU per second, which is `stathz`. Dividing by `hz` to turn ticks into seconds
  would be an eightfold error, and nothing about the result would look wrong. The capture
  exists so the two numbers are visible side by side rather than one being assumed
  (JD-033). The pairs are read by name, never by position: the braces and the field list
  are a `printf` in the kernel.
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
