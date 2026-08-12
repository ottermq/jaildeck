# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

Jail Deck is in early implementation. A working skeleton exists: HTTP server, routing, jail listing, jail start/stop/restart, and operation history logging are all implemented and wired end-to-end (roughly Phase 2–4 of `docs/ROADMAP.md`). It is **not** feature-complete — storage (ZFS), in-jail service management, and config editing (Phases 5–7) do not exist yet.

Five planning docs in `docs/` remain the source of truth for scope and design decisions and should be read before making non-trivial changes:

- **`docs/DECISIONS.md`** — locked architectural decisions (JD-001..JD-012). **This is the highest-priority doc**: where it conflicts with `SPEC.md`, `ARCHITECTURE.md`, or `UI.md` (which describe earlier/broader thinking), DECISIONS.md wins. Do not revisit a locked decision without clear justification, and flag it explicitly if you think one needs revisiting.
- **`docs/SPEC.md`** — product philosophy, what Jail Deck is/is not, MVP scope, baseline technical choices, open product questions
- **`docs/ARCHITECTURE.md`** — layered architecture, directory layout, command execution model, adapter responsibilities, open architecture questions
- **`docs/ROADMAP.md`** — phased build order (Phase 0 research → Phase 8 packaging); work should generally follow this sequence rather than jumping ahead
- **`docs/UI.md`** — page structure, component strategy, HTMX interaction patterns, copywriting guidelines

When these docs conflict with an instruction or assumption, treat the docs as authoritative and flag the conflict rather than silently picking one.

### Jail discovery and binding — resolved past the original MVP scope

Two decisions in `docs/DECISIONS.md` were deliberately updated after the code outgrew the original wording (not a drift to silently correct):

- **JD-004 (Jail Discovery)** now documents what `internal/system/freebsd/jail_helper.go` and `jail.go` (`List`/`mergeJails`) actually do: merge `jls` (running) with `/etc/jail.conf` + `/etc/jail.conf.d/*.conf` (configured), so a configured-but-stopped jail is still visible. The original JD-004 text (jls-only) was locked before it was clear that made stopped jails invisible.
- **JD-002 (Binding Model)** now defaults to `127.0.0.1` (`internal/config/config.go`) but allows operators to explicitly set `JAILDECK_HOST` to another interface (e.g. `0.0.0.0`) for development or trusted-network access — at their own risk, since there's still no auth (JD-003). Don't treat a non-localhost `JAILDECK_HOST` as a bug; it's an intentional operator opt-in.

## Commands

```sh
make build   # go build with version ldflags, output at bin/jaildeck
make run     # build then run the binary
go run ./cmd/jaildeck            # run without a build step
go test ./...                    # run all tests
go test ./internal/system/freebsd/... -run TestParseJLSOutput_Success  # single test
go vet ./...
```

There is no lint config beyond `go vet`. Config is loaded from environment variables (optionally via a `.env` file, see `.env.sample`): `JAILDECK_HOST` (default `127.0.0.1`, see binding note below) and `JAILDECK_PORT` (default `8888`; local `.env` uses `3333`). The app logs mutating operations to `jaildeck-operations.log` in the working directory (gitignored).

Only `internal/system/freebsd` requires a real FreeBSD host to fully verify (it shells out to `jls`/`service` and reads `/etc/jail.conf*`). Everything else builds, vets, and tests on any platform — the parsing logic in `jail_helper.go` and `jail.go` is unit-testable anywhere since it operates on strings/fixtures, not live system state.

## Architecture

Strict one-directional layering: `handlers` → `services` → `system` (with `system/freebsd` as the concrete adapter). Handlers never shell out directly.

```
cmd/jaildeck/main.go        # loads config, builds App, starts http.ListenAndServe

internal/
  app/app.go                 # composition root: wires adapter → service → handler,
                              # registers all chi routes (the only place that knows
                              # the full dependency graph)
  config/                    # env var loading (JAILDECK_HOST, JAILDECK_PORT)
  domain/jail.go              # Jail, JailStatus — plain data, no behavior
  handlers/                  # parse HTTP (chi params, query params), call services,
                              # render page or HTMX component via views.Renderer
  services/
    jail_service.go           # validates jail names, calls system.JailSystem,
                               # logs every mutation to operations.Logger
    operation_service.go      # translates raw query params into an operations.Filter
  system/
    jail.go                   # JailSystem interface (List/Start/Stop/Restart)
    fake.go                   # FakeJailSystem — in-memory, used when swapped in for
                               # non-FreeBSD dev (see the commented-out line in app.go)
    command.go                # Command, CommandResult, CommandRunner interface,
                               # CommandError (wraps command + args + result + cause)
    exec_runner.go             # ExecCommandRunner — the only place os/exec is called
    freebsd/
      adapter.go                # Adapter{runner}, implements system.JailSystem
      jail.go                   # List (merges configured + running), Start/Stop/Restart
                                 # via `service jail <action> <name>`, jls JSON parsing
      jail_helper.go             # jail.conf / jail.conf.d parsing (JD-004),
                                  # config+running merge, failure-message scraping
  operations/
    logger.go                  # Entry, Filter, Logger/Reader interfaces
    file_logger.go              # FileLogger — append-only JSON-lines file, mutex-guarded;
                                 # Recent() reads the whole file, filters, tails to limit,
                                 # reverses to newest-first
  views/renderer.go            # Renderer holds one *template.Template per page, each
                                 # pre-parsed with layout + its own components;
                                 # Render() executes "layouts/base.html",
                                 # RenderComponent() executes a named component template
                                 # for HTMX fragment responses

web/
  templates/layouts/base.html
  templates/pages/{jails,operations}.html
  templates/components/{jail_row,jail_action_result,operation_filters}.html
  static/css/app.css
```

Notes for extending this:

- **Adding a page/component**: `views.Renderer` parses templates per-page up front in `NewRenderer()` — a new page or component needs its own entry in that constructor (there's no glob-based auto-discovery).
- **Command execution**: everything that shells out goes through `system.CommandRunner.Run(ctx, system.Command{Name, Args})` — argument arrays, not string interpolation. `ExecCommandRunner` is the only real implementation; there is no allowlist enforcement yet (see `ARCHITECTURE.md` for the intended design).
- **Error handling for operations**: adapter failures are wrapped in `system.CommandError` (command, args, raw result, underlying cause). Handlers use `errors.As` to pull out `CommandError` and show `.Summary()`; `jail_service.go`'s `logJailOperation` does the same to populate `operations.Entry.Command`/`ExitCode`/`Error`. Follow this pattern rather than stringifying generic errors when adding new mutating operations.
- **HTMX responses**: mutating jail endpoints (`/jails/{name}/start|stop|restart`) return `components/jail_action_result.html`, which re-renders the jail's table row (`hx-swap="outerHTML"` on `#jail-{name}`) plus an out-of-band swap (`hx-swap-oob="true"`) into `#operation-result` for the message banner. Follow this two-target pattern (row + OOB banner) for new row-level actions rather than inventing a new mechanism.
- **Filters**: operation history filtering flows query params (`handlers/operations.go`) → a `map[string]string` → `services.OperationService.Recent` → `operations.Filter` → `FileLogger.applyFilters`. If you add a new filterable field, thread it through all four of those, not just the query-string parsing.
- No CSRF protection, command allowlist, or confirmation dialogs exist yet despite being called for in `ROADMAP.md` Phase 3 — don't assume they're there when reasoning about safety of mutating routes.
