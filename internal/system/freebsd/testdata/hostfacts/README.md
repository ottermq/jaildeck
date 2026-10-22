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

"PF is not loaded" is an *empty* stdout, a message on stderr and a non-zero exit. A
parser that only ever sees stdout cannot tell it apart from "PF is loaded and disabled",
and would report the wrong thing about the host's firewall.

`MANIFEST` lists every command with the exit status it returned.

## Why these are committed

Parsing is the one part of the FreeBSD adapters that cannot be written against the fake
adapter. The fake exists so handlers, services and the web interface can be developed
without a FreeBSD host; it says nothing about what `zfs list` actually prints. Hand-typed
fixtures are guesses, and guesses here produce a parser that is confidently wrong against
the real thing — see "What the real output corrected" below.

## Re-capturing

`capture.sh` is read-only; it changes nothing on the host. Run it on any FreeBSD box and
copy the result into a directory named after that host:

```sh
ssh <host> 'sh -s' < capture.sh
scp -r <host>:/tmp/jd-capture ./<hostname>
```

## Hosts

### `vm-15.1/` — 15.1-RELEASE-p3, captured 2026-10-21

The laptop's libvirt VM (`freebsd-jails`, ssh alias `fbsd`). A host set up for jails and
nothing else, which makes it the "already provisioned" case for #21 and a failing case
for most of #1:

| Check | State here | Why |
| --- | --- | --- |
| `exec_timeout` | **pass** | `/etc/jail.conf` sets `exec.timeout = 120` |
| `zfs_pool` | **pass** | one pool, `zroot` |
| `zfs_layout` | **pass** | `zroot/jails` at `/usr/local/jails`, with `containers`, `media`, `templates` |
| `cpu_virtualization` | **pass** | `VT-x` with both `EPT` and `UG` |
| `racct` | **fail** | `kern.racct.enable` is `0` |
| `bridges` | **fail** | only `vtnet0` and `lo0` exist |
| `devfs_rulesets` | **fail** | `/etc/devfs.rules` does not exist |
| `pf` | **fail** | `pf.ko` is not loaded |

**Two caveats.** This is a KVM guest, so its `VT-x` line is what KVM exposes for nested
virtualisation and may differ from bare metal — the `vms` check wants a capture from a
physical host before it is trusted. And the capture ran as an unprivileged user, which is
why `/etc/defaults/devfs.rules` is `Permission denied` rather than its contents; Jail
Deck runs as root and would read it. That permission case is still worth keeping, because
it is the shape an `unknown` must be derived from.

### Still missing

No host here produces these, and inventing them is what this directory exists to avoid:

- any check in the **`unknown`** state other than the devfs permission case
- `racct` enabled, a real bridge, an installed devfs ruleset, PF loaded — the **passing**
  side of four checks
- `VT-x: (disabled in BIOS)`, and a host with no `VT-x` line at all
- the write path: `zfs create` output, and a dataset mounted somewhere unexpected (#21's
  `409`)

`isengard` should cover most of the first two. Until it is captured, a parser must fall
through to `unknown` on anything it does not recognise — not because the format is
unknown, but because a report that says "I could not tell" is correct where a wrong
`fail` sends someone to reboot a working machine.

## What the real output corrected

Every one of these was wrong in a hand-written fixture before the capture:

- **The `VT-x:` line is indented two spaces.** `strings.HasPrefix(line, "VT-x:")` finds
  nothing on a real host. Trim first.
- **Its feature list includes `VPID`**, and the order is not what you would guess, so
  features must be split on `,` and compared as whole tokens — never with
  `strings.Contains` over the line, and never over the whole buffer: `UG` is a substring
  of `root HUB`, which appears in `dmesg` several times.
- **`/etc/devfs.rules` does not exist on a stock install.** Absent is the *normal* state
  and a clean `fail` with a remedy; `EACCES` is an `unknown`. They are different answers.
- **`ifconfig -g bridge` with no bridges exits 0 with empty stdout** — not an error.
- **`zfs list` naming several datasets where one is missing exits 1 and still prints the
  others on stdout.** Treating a non-zero exit as total failure silently loses the
  datasets that do exist. Hence `-r <pool>`, one call, match in Go.
- **A pool's own mountpoint can be `none`** (`zroot` is), so the layout check compares
  the *parent's* mountpoint to `/usr/local/jails` and must not assume every dataset has a
  filesystem path.
- **`dmesg` cannot be piped.** `system.CommandRunner` takes a name and an argument array
  with no shell, so there is no `dmesg | grep`: read the whole buffer (7.7 KB, 144 lines
  here) and filter in Go.
