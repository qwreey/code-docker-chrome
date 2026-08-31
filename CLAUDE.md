# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A **provider**: a container that attaches a Chrome browser to
[code-docker](https://github.com/qwreey/code-docker). There is no application source
here — it is infrastructure/config (Dockerfile, compose overlay, shell service scripts)
plus one small Go program, `cdp-bridge/`.

The thing that makes it worth existing is not "Chrome in Docker" — several good images
already do that — but that **one browser is reachable two ways at once**: an agent
drives it over CDP while a person watches and clicks in the same session over VNC. A
login completed by hand is a login Claude then has.

## The one structural decision to not undo

**There is no standalone `docker-compose.yml`, only `code-docker-chrome.yml`.**

roblox-studio-docker — the sibling project this repo's conventions come from — keeps
both, because Roblox Studio in a container is worth having on its own and code-docker is
an optional consumer. That is not true here. This repo is the wiring, so the overlay is
the only compose file and there is nothing to opt out of. Consequences that follow from
it, and that look like oversights if you don't know why:

- `NETINIT_WAIT` and `DNS_LOCAL_ENABLED` default to **on** in `entrypoint.sh`.
  roblox-studio-docker defaults them off and its overlay turns them on, because it has a
  standalone mode with a working `127.0.0.11` and no router to wait for. There is no
  such deployment here.
- No `ports:` anywhere, so nothing to `!reset`.
- `.env.example` documents variables read from **code-docker's** `.env`, not from a
  compose file in this directory. There isn't one to load an env file for.

## Architecture

```
[code-docker]                                  [code-docker-chrome]
chrome-devtools-mcp
  --browser-url http://127.0.0.1:9222
        │  Host: 127.0.0.1:9222
        ▼
  cdp-bridge unwrap (loopback)                 cdp-bridge wrap (chrome-cdp:9223)
    + Authorization: Bearer $TOKEN  ──────────▶   verify bearer, strip it
    Host passed through                              │ Host passed through
                                                     ▼
                                              Chrome (127.0.0.1:9222)
                                              labwc + wayvnc
                                                     │
                               chrome-vnc:5900 ──────┴──▶ code-docker-router ──▶ person
```

### Host passthrough is the mechanism, not an implementation detail

Chrome rewrites `webSocketDebuggerUrl` to match the `Host` header it was asked with. Both
proxies leave `Host` alone (`r.Out.Host = r.In.Host` in `cdp-bridge/main.go`), so a client
that reaches unwrap on `127.0.0.1:9222` is told the socket is at
`ws://127.0.0.1:9222/...` — pointing back at unwrap. CDP therefore looks entirely local
and `chrome-devtools-mcp` needs no remote-aware flags.

Two consequences:

- **Do not "fix" the proxies to rewrite Host.** That breaks the whole design.
- Chrome's DevTools endpoint refuses a Host that is neither an IP nor localhost
  (DNS-rebinding protection), so reaching `wrap` directly by service name answers
  `Host header is specified and is not an IP address or localhost.` even with a valid
  token. That is expected; the supported path never sends such a Host. Debug with
  `curl -H "Host: 127.0.0.1:9222"`.

### Two networks, for opposite reasons

| Network | Members | Shape |
|---|---|---|
| `code-docker-internal` | chrome, code-docker, dind, … | **reused as-is**; CDP is gated by a token |
| `chrome-vnc` (`internal: true`) | chrome, router | **its own**; a subtraction — code-docker is excluded |

A dedicated CDP network was considered and **rejected**: code-docker would have to join
it, which would make a provider edit the main project's topology. The token gets the same
result without that. Verified: dind is on `code-docker-internal` and *can* reach chrome by
IP — and gets 401.

The VNC network is the opposite shape, and is the same pattern roblox-studio-docker uses:
only router joins, so screen access never becomes a second unaudited control path beside
CDP. Both listeners resolve a per-network alias with `getent` and **fail closed** rather
than binding `0.0.0.0` (`config/supervisor/resolve-bind-alias.sh`); a silent fallback
would undo exactly this split.

`chrome-vnc` carries **both** netinit labels. `netinit.provider` is what opts a network in
to the agent at all — matching is fail-closed, so `netinit.exempt-forward` alone is
inert and the DOCKER-USER exemption silently never applies. `netinit.gateway` is
deliberately absent: a relay path to a person is not an egress path.

## Chrome runs with `--no-sandbox`

Two independent reasons, both measured rather than assumed:

1. The container runs as root and Chrome refuses to start its zygote sandbox as root at
   all — `Running as root without --no-sandbox is not supported` (crbug.com/638180).
   Relaxing seccomp does not help; it is a uid check.
2. Docker's default seccomp profile blocks `clone(CLONE_NEWUSER)`, so even as a non-root
   user the namespace sandbox fails with `No usable sandbox!` unless the profile is
   relaxed or `CAP_SYS_ADMIN` granted — which a nested LXC host may not be able to give.
   (`--user 1000 --cap-drop ALL --security-opt seccomp=unconfined` does work, if that
   trade is ever wanted.)

linuxserver/docker-chromium — desktop, labwc and VNC, the closest comparable image —
hardcodes the same flag. The boundary here is the container plus the two-network split.

`/dev/dri` is deliberately not passed through: software rendering is enough for what this
does, and a DRM render node is a large ioctl surface straight into the host kernel.

## Process supervision

`entrypoint.sh` does the once-only setup and `exec`s supervisord; everything after that is
a `[program:...]` file under `config/supervisord.d/`, one per process, with its service
script in `config/supervisor/` — the same split code-docker and roblox-studio-docker use.
`config/supervisor/` flattens into `/etc/code-docker-chrome/` at build time, so scripts
reference stable paths.

`dbus`, `labwc` and `wayvnc` set `autorestart=false` + `startretries=0` so a first failure
surfaces to `critical-watchdog` immediately instead of being retried into a crash loop;
that program polls and shuts the whole stack down, because supervisord has no "tear
everything down if X exits" directive.

**`chromium` is deliberately not in the watchdog's list.** A person at the VNC session
closing the last window is ordinary; `autorestart=true` brings it back. Killing the
container over it would make the GUI a trap. A compositor dying is different — nothing
can be fixed from the inside.

## cdp-bridge is built twice

Same `cdp-bridge/main.go`, two places:

| Half | When | Where | Output |
|---|---|---|---|
| `wrap` | `docker compose build code-docker-chrome` | Dockerfile stage 1, `golang:1.27-alpine` | `/usr/local/bin/cdp-bridge` in the image |
| `unwrap` | `code-docker/install.sh` | inside the running code-docker, mise's go 1.27 | `/code/.local/bin/cdp-bridge` |

`unwrap` builds from the working tree the overlay bind-mounts read-only at
`/run/code-docker-chrome/`; `wrap` is a snapshot from image build time. **Editing
`cdp-bridge` needs both** a `docker compose build` and a re-run of `install.sh`. Keep
`GO_VERSION` in `install.sh` matching the Dockerfile's `FROM golang:` tag.

Not fetched over the network on purpose: `@latest` would resolve against the default
branch rather than the checkout that built the image, mise's go backend needs tags this
repo does not have, and building locally works with no egress at all.

`install.sh` uses `mise exec go@…` rather than `mise use -g`. Go is a build dependency of
one command; writing it into `~/.config/mise/config.toml` would be this repo editing a
file that belongs to the person using the container. **node is the exception** and is
installed globally, because `npx chrome-devtools-mcp` is run later by Claude Code itself
and needs to be on the container's PATH.

## Conventions

- Service scripts are `#!/usr/bin/env bash` + `set -eu`, and `exec` their long-running
  program last so supervisord's stop signal reaches it directly. Post-start work that
  needs the compositor goes in a background subshell before the `exec` (see
  `labwc-service.sh`).
- Shared helpers (`wait-for-wayland.sh`, `resolve-bind-alias.sh`) are **sourced, not
  exec'd**, and are not executable. supervisord runs each program as its own process with
  no shared state, so several services need the same helper independently.
- `entrypoint.sh` wipes `$XDG_RUNTIME_DIR` on every start: `docker restart` reuses the
  writable layer, so a stale `wayland-N` socket from a crashed labwc would satisfy the
  wait loop instantly and leave clients dialing a dead server.
- Comments carry the *why*, especially where a value looks arbitrary or a guard looks
  paranoid. Most of them record something that was measured.

## Integration with code-docker

Attached through `ootb-manifest.env` → code-docker's `ootb.sh`/`migrate.sh`, which writes
`extra-include.yml`, sets `EXTRA_INCLUDE`, merges `chrome-vnc` into
`.env.router`'s `ROUTER_EXTRA_ALLOWED_TARGET_HOSTS`, and generates `CHROME_CDP_TOKEN`.
code-docker never learns this project's name.

The overlay also merges into services it does not define — that is how the token reaches
`code-docker`, how `install.sh` reaches it (a read-only mount at
`/run/code-docker-chrome/`), and how router joins `chrome-vnc`. Include merging is by
service name and has no "only services this file defines" restriction.

`install.sh` depends on a code-docker new enough to have the on-volume supervisord include
(`/code/.local/share/code-docker/supervisord/*.conf`) and `bin/reload-services` — added in
code-docker commit `a431928` for this.
