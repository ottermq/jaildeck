---
title: "API guide"
description: "Authenticating, the ZFS property allowlist, snapshots, and keeping data past the jail that held it."
---

# The Jail Deck API

Every endpoint lives under `/api`. The machine-readable description is
[`swagger.json`](./swagger.json), generated from the handlers themselves — `make doc`
regenerates it, and `make check` fails if the committed copy is not what the annotations
produce.

Import it into Insomnia, Bruno, Postman or anything else that reads Swagger 2.0. Jail Deck
does not serve a Swagger UI: the spec is a file, and the tool you already use is better at
browsing it than a page embedded in a jail host would be.

This page is the part a spec cannot hold — the tables, the worked flows, and the things that
were measured rather than assumed.

## Authenticating

Two ways, and the header wins:

```sh
# A session, as the browser does
curl -c jar -X POST .../api/auth/login -d '{"password":"..."}'

# A token, as a script does
jaildeck token add insomnium          # shown once
curl -H "Authorization: Bearer $TOKEN" .../api/jails
```

The `Authorization` header is checked **before** any cookie and returns 401 rather than
falling back to one, so a token client fails cleanly even while a session is active. Tokens
set no cookie and are immune to CSRF by construction.

Sessions live in memory and last 12 hours, so restarting the service signs every browser out.
Tokens are stored and survive.

**The session cookie is `Secure`.** A browser discards it over plain HTTP on a non-loopback
origin — so pointing `JAILDECK_HOST` at `0.0.0.0` and browsing to the host's IP gives you a
login that appears to succeed and then 401s on the next request. Reach a remote host through
an SSH forward instead (JD-002):

```sh
ssh -L 8888:localhost:8888 isengard
```

## Jails carry their storage

`GET /api/jails` and `GET /api/jails/{name}` include each jail's ZFS datasets and the user
properties of its own dataset:

```json
{ "name": "ottermq", "path": "/usr/local/jails/containers/ottermq",
  "datasets": [ { "name": "tank/jails/containers/ottermq",
                  "mountpoint": "/usr/local/jails/containers/ottermq",
                  "used": 31850496, "referenced": 416907264, "available": 963201556480 } ],
  "properties": { "otter:instance": "mq-01" } }
```

A dataset is the jail's when it is **mounted at the jail's path or under it** — not by naming
convention, so it works for a jail placed outside `<pool>/jails/containers/<name>` and reports
nothing for a jail that is not on ZFS at all (JD-022).

Sizes are **bytes**, not the `1.41G` that `zfs list` prints, so they can be compared and
totalled. `referenced` routinely exceeds `used` on a jail cloned from a template, because a
clone still refers to the template's blocks — which is why a quota belongs on a child dataset.

Both keys are **absent** for a jail with no dataset of its own, and also when the pool could
not be read. That failure is logged at `WARN` rather than failing the request, because seeing
a stopped jail is how you start it. The cost, accepted deliberately: absent storage means
either "none" or "could not read", and only the log says which.

## Which ZFS properties may be set

Five of the sixty-odd this ZFS reports as editable, and `mountpoint` only at creation
(JD-023):

| property | `zfs create` | change later | why |
| --- | --- | --- | --- |
| `mountpoint` | yes | **no** | changing it under a running jail leaves the jail looking at a directory no longer backed by that dataset |
| `refquota` | yes | yes | growing a quota is an ordinary need with no other path |
| `recordsize` | yes | yes | affects new writes only |
| `compression` | yes | yes | |
| `atime` | yes | yes | |

Anything else is `400` naming the list. The ones worth knowing about:

- **`sharenfs`** would publish the jail's filesystem over NFS.
- **`canmount`** would stop it mounting.
- **`exec`, `setuid`, `devices`** are mount-time security semantics.
- **`jailed`** is ZFS delegation.
- **`quota`** is deliberately absent: a clone's `referenced` space includes the template, so
  the limit belongs on `refquota` (JD-007). Offering both invites the mistake that decision
  exists to prevent.

Values are **not** validated here. `zfs` answers `recordsize=7K` with
`must be power of 2 from 512B to 16M`, and that sentence is what comes back — as a `400`, not
a `500`, so a client does not retry a value that can never work.

### The quota lands where it belongs

Measured on the deployment target, on a jail with a 2G `refquota` on its `data` dataset:

```
# jexec ottermq df -h /data
Filesystem                            Size    Used   Avail Capacity  Mounted on
tank/jails/containers/ottermq/data    2.0G     96K    2.0G     0%    [restricted]
```

2.0G inside the jail, not the pool's 897G. (`[restricted]` is `enforce_statfs` hiding the host
path from the jail.)

## Tagging, and why it is a separate verb

`PATCH`, not `PUT`: tags **merge**. A replacing write would strip a tag another tool had set,
and on a real host `zrepl:placeholder` and `com.sun:auto-snapshot` both live in this namespace.

```sh
curl -X PATCH .../api/jails/ottermq/properties \
  -d '{"otter:instance":"mq-01","otter:retired":null}'
```

`null` removes the property via `zfs inherit`, which is **not** the same as setting it empty:
`"otter:tier": null` means the jail has no tier; `"otter:tier": ""` means its tier is the
empty string.

Only user properties — the name must contain a colon — and the value is passed through
untouched (JD-011). A jail's listing reports **its own** dataset's properties, never a
child's: tag a child with `otter:instance` and it will not appear on the jail, or one jail
with three children would look like four instances to a client searching by tag.

## Snapshots

A jail-level snapshot is **recursive and atomic** — `zfs snapshot -r` takes the jail and every
child at one point in time, which is what lets a root be re-cloned while its data stays
consistent with it. Measured:

```
tank/jails/containers/ottermq@before-upgrade        createtxg 186346
tank/jails/containers/ottermq/data@before-upgrade   createtxg 186346
```

One transaction, from one call.

**A rollback is never recursive**, because ZFS offers no way to make it so: `zfs rollback` has
no flag that descends into children, and its own `-r` means *"destroy the snapshots newer than
this one"*. A jail-level rollback therefore leaves the children where they are, and a child is
rolled back through its own route, as a separate decision (JD-024).

Snapshots are ordered by **`create_txg`**, ZFS's transaction group, not by `created`. A
timestamp has second granularity and two snapshots taken in the same second tie on it — and
"which of these is newer" is precisely what decides what a rollback destroys.

### Refusals happen before anything destructive runs

What blocks a destroy is asked for with `zfs get clones` and `zfs holds`, rather than read out
of the error `zfs destroy` would have raised. A check that happens first is a refusal; one
that happens after is a report of something already attempted.

```
409 blocked.newer   rolling back would destroy these — pass destroy_newer=true
409 blocked.holds   "...@nightly (keep-for-audit)"
409 blocked.clones  "tank/jails/containers/ottermq-copy"
```

Newer snapshots can be accepted with `destroy_newer=true`. **Clones and holds cannot be
overridden here at all**: getting past a clone needs `zfs destroy -R`, which takes the clone
with it, and a hold exists precisely to say "not this one".

`confirm=` is the snapshot's **full** `dataset@snapshot`, not the word already in the path, so
a script looping over jails with one snapshot name in a variable cannot confirm every one of
them by accident (JD-016).

## Destroy the jail, keep the data

`zfs destroy -r` on a jail takes its children and their snapshots with it, so a dataset worth
keeping has to stop being a child first (JD-025).

```sh
curl -X POST .../api/jails/ottermq/datasets/data/retain -d '{"name":"ottermq-data"}'
# {"to":"tank/jails/retained/ottermq-data","remounted":true}

# ... destroy and recreate the jail ...

curl -X POST .../api/jails/ottermq/datasets/data/restore -d '{"from":"ottermq-data"}'
```

Three commands under the hood, each audited separately:

```
zfs rename -u  <jail>/data  <pool>/jails/retained/<name>
zfs unmount                 <pool>/jails/retained/<name>
zfs mount                   <pool>/jails/retained/<name>
```

**The `-u` is not a style preference.** A plain `zfs rename` unmounts and remounts inside the
same command. On a running jail that is the filesystem disappearing from under a live process,
decided by `zfs` rather than by anyone:

```
plain zfs rename  ->  ls: /data: No such file or directory   (inside the RUNNING jail)
zfs rename -u     ->  mount untouched; the jail keeps working
```

**But the move still completes in the same call**, because `-u` alone leaves the dataset named
in one place and mounted in another — and a jail with a foreign filesystem mounted inside it
cannot be destroyed:

```
zfs destroy -r tank/jails/containers/probe-jail
  cannot unmount '/usr/local/jails/containers/probe-jail': pool or dataset is busy
```

So `remounted: true` means the filesystem moved and a running jail just lost that path. Stop
it first.

`<pool>/jails/retained` is created the first time one is needed and is deliberately **not**
part of the layout readiness check: a host that has never retained anything is not missing
something (JD-021).

## Templates

A jail is always a clone of a template, so the only question is **when** you provision it:

| | clone from | after it starts |
| --- | --- | --- |
| **a prepared jail** | a template that already has what you need | it works |
| **a custom jail** | a release template, a bare userland | you fill it |

There are two ways to get a prepared template.

### Clone a jail you configured

The general one, and the FreeBSD Handbook's own (17.4.3). Configure a jail however you like —
by hand, over ssh, through exec — then stop it and promote it:

```sh
curl -X POST .../api/jails/ottermq/stop
curl -X POST .../api/jails/ottermq/template -d '{"name":"ottermq-base"}'
```

```
zfs snapshot <jail>@base
zfs clone    <jail>@base  templates/ottermq-base
zfs promote  templates/ottermq-base
```

It captures whatever is in the jail, not only packages: `sysrc`, an edited config, an
`initdb`.

**The promote is the step that matters most and is easiest to miss.** Without it the template
is a clone *of* the jail, and that jail can never be destroyed:

```
zfs destroy containers/ottermq
  cannot destroy: filesystem has dependent clones  ->  templates/ottermq-base
```

After it, the jail is a clone of the template instead — the right way round, since a template
should outlive the jail it came from. The lineage becomes
`15.1-RELEASE → ottermq-base → ottermq`, so the new template lists like any other.

**The jail must be stopped**, and this refuses rather than stopping it for you. A snapshot of
a running jail succeeds but is an image caught mid-stride; the Handbook says stop it first,
and taking a service down is your call. A jail already carrying a `@base` is also refused —
reusing it would make a template of whenever *that* was taken, not of the jail now.

### Install packages, without a jail


A jail's root is a clone of a template's `@base` snapshot, so a **derived template** — a
release template plus packages, re-snapshotted — means every jail cloned from it starts with
those packages installed (JD-027):

```sh
curl -X PUT .../api/templates/postgres-16 \
  -d '{"from":"15.1-RELEASE","packages":["postgresql16-server"]}'
```

```
zfs clone    templates/15.1-RELEASE@base templates/postgres-16
pkg -c       /usr/local/jails/templates/postgres-16 install -y postgresql16-server
zfs snapshot templates/postgres-16@base
```

`pkg -c` chroots, so nothing is started — no jail, no stanza, no networking for a temporary
jail, nothing to clean up. It is the same shape as the `freebsd-update -b <root>` that JD-007
already runs against an unbooted template.

**It installs packages and nothing else.** `sysrc`, a configuration file, an `initdb` — those
belong to the other path, which is also the general one: clone a template, create a jail on
it, `exec` into it, stop it, and snapshot the dataset. A jail is always a clone of a template,
so the choice is only ever *when* you provision it:

| | clone from | after it starts |
| --- | --- | --- |
| **a prepared jail** | a template with the packages already in it | it works |
| **a custom jail** | a release template, a bare userland | you fill it |

`GET /api/templates` lists them with `ready` and `parent`. Neither is stored: `ready` is
whether `@base` exists, which is what makes a template usable, and `parent` is read back from
`zfs get origin`. A derived template therefore cannot disagree about where it came from.

### A failed install needs no cleanup

If `pkg` fails, the clone stays **without** its `@base`. It is a template that exists and is
not ready, which is exactly what it is and exactly what the listing says. Nothing has to be
tidied up for that state to be truthful.

A second attempt on it is refused rather than resumed: finishing it would install packages on
top of whatever the failed attempt left behind, and a clean clone is indistinguishable from a
clone with half of postgres in it once you are outside looking in. Destroy it and start again.

### Package names

Only one rule: an argument beginning with a dash is refused, because `pkg` would read it as an
option and `install -y -f` is not the command that was asked for. No shell is involved either
way. Versions, origins and flavours pass through untouched — `pkg` knows what a package name
is and says so precisely when it is wrong, and that sentence comes back as a `400`.

### Removing a template

```sh
curl -X DELETE '.../api/templates/postgres-16?confirm=postgres-16'
```

**409 while anything is cloned from it**, naming every one:

```json
{"template":"tank/jails/templates/15.1-RELEASE",
 "blocked":{"reason":"refused: 3 dataset(s) are cloned from it. Destroying it would take
             them with it, so each has to go first -- or, for a jail that should outlive
             the template, zfs promote moves the dependency the other way.",
            "clones":["tank/jails/containers/goodiesdb","tank/jails/containers/ottermq",
                      "tank/jails/containers/postgres"]}}
```

ZFS would refuse as well, but its message says "dependent clones" and leaves you to work out
which. This asks `zfs get clones` first, so the refusal is an answer.

It is also how you clean up the half-built template a failed derive leaves behind.

## Upgrading a jail

A jail's root is a clone of a template snapshot, so upgrading one is replacing that root with
a clone of a newer snapshot while its child datasets survive (JD-026):

```sh
curl -X POST '.../api/jails/ottermq/reclone?confirm=ottermq' \
  -d '{"template":"tank/jails/templates/15.1-RELEASE@p5"}'
```

```json
{"root":"tank/jails/containers/ottermq",
 "from":"tank/jails/templates/15.1-RELEASE@base",
 "to":"tank/jails/templates/15.1-RELEASE@p5",
 "old_root":"tank/jails/retained/ottermq-root-base",
 "children":["tank/jails/containers/ottermq/data"],
 "running":true}
```

The jail is stopped and started again by the call, and ends in the state it was found in.
**Nothing is destroyed** — the old root is parked beside the children, so an upgrade onto the
wrong template costs one call to undo.

The template must be the full `dataset@snapshot`. There is no default, because every jail on a
real host is already a clone of `templates/<release>@base` and defaulting to it would re-clone
a jail onto the snapshot it is already on.

### What survives, and what does not

Measured on a jail upgraded this way:

```
inside the jail:   /data/marker          state-that-must-survive-the-upgrade
in the parked root: root/marker          i-was-written-into-the-root
in the new root:    root/marker          No such file or directory
```

The child dataset came back with its contents and its `refquota`. Anything written into the
jail's **own root** did not — the root is a fresh template now. That is the point of the
exercise, and it is why state belongs in a child dataset (JD-007).

The old root is still readable at the parked path, which is why it is kept rather than
destroyed. It is a clone, so it occupies only what the jail wrote since it was made: 352 KB
against a 372 MB template, in that run.

### If a step fails

Nothing is retried and nothing is undone. The response names the step and lists every dataset
that is no longer under the jail:

```json
{"failed_at":"clone",
 "parked":[{"was":"tank/.../ottermq/data","now":"tank/jails/retained/ottermq-data"},
           {"was":"tank/.../ottermq",     "now":"tank/jails/retained/ottermq-root-base"}]}
```

Restore them with the call above. An automatic undo was rejected deliberately: when the undo
also fails you have two interleaved failures and a host in a state neither describes.

Refusals happen **before** the jail is stopped — a mistyped template name costs an error, not
an outage.

## Correlating with your own jobs

Send `X-Request-Id` and every audit entry the request produces carries it, so
`GET /api/audit?request_id=...` lines your job up against what Jail Deck did:

```
create-dataset  rid=otter.job.5  zfs create tank/jails/retained
rename-dataset  rid=otter.job.5  zfs rename -u tank/.../data tank/jails/retained/probe5-data
mount-dataset   rid=otter.job.5  zfs unmount tank/jails/retained/probe5-data
mount-dataset   rid=otter.job.5  zfs mount tank/jails/retained/probe5-data
```

Up to 64 characters of ASCII letters, digits and `-._:` — a UUID, a ULID or a `trace:span`
pair. Anything else is **dropped rather than rejected**, with the reason logged: the operation
still succeeds and its entries carry no id. Failing an operator's jail restart because their
correlation id held a space would be the worse outcome.

The filter matches exactly and case-sensitively, since the id is the client's own opaque
string and not a name Jail Deck normalises.
