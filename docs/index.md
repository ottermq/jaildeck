---
title: "Jail Deck"
description: "A FreeBSD jails operations dashboard and host agent, written in Go"
---

# Jail Deck

A lightweight operations dashboard and host agent for FreeBSD jails.

Jail Deck is a thin layer over the tools FreeBSD already ships — `jls`, `service`,
`jail.conf`, `zfs` — exposed through a JSON API and an embedded web interface. One binary,
no database, no Node.js at runtime, and no abstraction that pretends a jail is a Linux
container.

> A personal project, written to learn Go and FreeBSD properly rather than to be a product.
> It is built for a single operator on hosts they administer directly, and it is also the
> host agent for Otter Labs Cloud.

- Source and issues: [github.com/ottermq/jaildeck](https://github.com/ottermq/jaildeck)
- Releases: [github.com/ottermq/jaildeck/releases](https://github.com/ottermq/jaildeck/releases)
- Getting started, configuration and deployment: [the README](https://github.com/ottermq/jaildeck#readme)

## Documentation

| | |
| --- | --- |
| [API guide](./api/) | Authenticating, the ZFS property allowlist, snapshots, and keeping data past the jail that held it |
| [`swagger.json`](./api/swagger.json) | The machine-readable API description, generated from the handlers |
| [Specification](./SPEC) | What Jail Deck is for, what is in scope, and the test a feature has to pass to belong here |
| [Architecture](./ARCHITECTURE) | How the code is laid out, and why |
| [Decisions](./DECISIONS) | Every locked decision, with its reasoning and the costs accepted |
| [Roadmap](./ROADMAP) | The milestones, what each delivers, and what it depends on |
| [Interface](./UI) | What the web interface shows |

## Where the project is

| Milestone | |
| --- | --- |
| Host Setup | done |
| Storage Visibility | done |
| Exec and Automation Contract | 2 of 3 — the exec endpoint remains |
| Storage Writes | 3 of 4 — re-cloning a jail's root remains |
| Templates | not started |
| Jail Provisioning | not started |
| Metrics, Firewall Visibility, Virtual Machines | not started |

Jails can be listed, started, stopped and restarted from the browser or the API; their ZFS
datasets and tags are visible and writable; snapshots can be taken, rolled back and
destroyed; and a dataset can outlive the jail that held it. Every operation is audited, and
nothing is reachable without a password or an API token.

## A note on how this is written

Jail Deck is a learning project, so the documentation records **why** more often than it
records what. The [decisions](./DECISIONS) are the clearest example: each one states what was
chosen, what it cost, and — where the answer came from a real host rather than from a manual
page — what was actually measured.

That habit has a practical edge. Several of the decisions here exist because something was
run on a FreeBSD box and did not do what the documentation implied:

- `zfs list -d 0 -t snapshot` returns **nothing** at exit 0, because a dataset's snapshots
  are its depth-**1** children.
- A plain `zfs rename` of a running jail's dataset makes `/data` vanish from inside that
  jail, mid-command.
- `rc.subr` reserves `${name}_program`, which silently overrides `$command`.

None of those were guesses that turned out wrong. They were assumptions that a test or a
probe caught, and writing them down is cheaper than rediscovering them.
