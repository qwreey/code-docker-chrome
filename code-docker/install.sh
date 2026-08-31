#!/usr/bin/env bash
set -euo pipefail

# Run this INSIDE code-docker (a code-server terminal, or `attach`). It installs the
# code-docker half of the Chrome integration:
#
#   1. the cdp-bridge binary, at a fixed path on the /code volume
#   2. the cdp-unwrap supervisord unit, into the on-volume unit directory code-docker
#      added for exactly this (its docs/index.md, "직접 만든 서비스를 supervisord에
#      올리기")
#   3. node, if missing, since chrome-devtools-mcp runs through npx
#
# Everything it writes lives on /code, so a container recreate keeps all of it.

UNIT_DIR="/code/.local/share/code-docker/supervisord"
BIN_DIR="/code/.local/bin"
BIN="${BIN_DIR}/cdp-bridge"
MISE="${HOME}/.local/bin/mise"
SRC_DIR="$(cd "$(dirname "$0")/../cdp-bridge" && pwd)"

if [[ ! -d "${UNIT_DIR}" ]]; then
  echo >&2 "install.sh: ${UNIT_DIR} does not exist. It is created by code-docker's entrypoint - are you running this inside code-docker, and is its image new enough to have the on-volume supervisord include?"
  exit 1
fi
if [[ -z "${CHROME_CDP_TOKEN:-}" ]]; then
  echo >&2 "install.sh: CHROME_CDP_TOKEN is empty in this container. code-docker-chrome.yml merges it into the code-docker service, so an empty value usually means the stack was not brought up with the overlay active (check EXTRA_INCLUDE in .env), or this container predates it and needs a recreate."
  exit 1
fi
if [[ ! -x "${MISE}" ]]; then
  echo >&2 "install.sh: mise not found at ${MISE}."
  exit 1
fi

mkdir -p "${BIN_DIR}"

# Built from the source the overlay mounts, not fetched over the network:
#
#   - `@latest` would resolve against this repo's default branch, while the wrap half
#     is baked into the chrome image from whatever checkout built that image. Building
#     ${SRC_DIR} keeps both halves coming from one working tree instead of two
#     independent points in the repo's history. It does NOT make them the same
#     revision - the mount is live and the image is a snapshot, so editing cdp-bridge
#     needs `docker compose build code-docker-chrome` as well as re-running this
#     script. Low stakes either way: the two ends speak plain HTTP proxying plus one
#     bearer header, not a wire protocol that evolves.
#   - mise's go backend resolves versions from tags and this repo has none, so
#     `mise use -g go:<this module>@latest` fails outright with "no versions found ...
#     matching date filter" (measured). Tagging would fix that, but a compose provider
#     is not a library and does not want a release cadence.
#   - and it works with no egress at all, which matters in a container whose whole
#     network policy is "the border is router's alone".
#
# ${BIN} is a fixed path on the volume because cdp-unwrap.conf names it literally -
# a supervisord unit should not depend on where a package manager put a shim today.
#
# GO_VERSION tracks the Dockerfile's `FROM golang:<version>-alpine`, so both halves are
# built by the same toolchain. `mise exec` rather than `mise use -g`: this is a build
# dependency of one command, not something the owner asked to have on their PATH, and
# writing it into ~/.config/mise/config.toml would be this script editing a file that
# belongs to the person using the container.
GO_VERSION="1.27"
echo "==> installing cdp-bridge (go ${GO_VERSION})"
( cd "${SRC_DIR}" && "${MISE}" exec "go@${GO_VERSION}" -- env CGO_ENABLED=0 go build -ldflags="-s -w" -o "${BIN}" . )
echo "    built ${SRC_DIR} -> ${BIN}"
"${BIN}" 2>&1 | head -1 || true

# node is the one thing that does go in globally, because it is not this script's build
# dependency - `npx chrome-devtools-mcp` is run later by Claude Code itself, so node has
# to be on the container's PATH, not just inside one subshell here. Only added when
# absent, and announced, since it does change the owner's mise config.
if "${MISE}" ls --installed 2>/dev/null | grep -q '^node\b'; then
  echo "==> node already installed"
else
  echo "==> installing node globally (mise use -g node@lts) - chrome-devtools-mcp runs through npx"
  "${MISE}" use -g node@lts
fi

echo "==> installing the cdp-unwrap supervisord unit"
mkdir -p /var/log/cdp-unwrap
install -m 644 "$(dirname "$0")/cdp-unwrap.conf" "${UNIT_DIR}/cdp-unwrap.conf"

echo "==> reloading supervisord"
reload-services
# `reload-services` is reread+update, which only acts on units whose *config* changed.
# On a re-run the unit is byte-identical and the rebuilt binary lands at the same path,
# so update does nothing and the old process keeps serving the old code. Restart
# explicitly - re-running this script is exactly how someone picks up an edit.
supervisorctl restart cdp-unwrap

cat <<'MSG'

Done. Check it reached Chrome:

    curl -s http://127.0.0.1:9222/json/version

Then register the MCP server (once per project, or -s user for all of them):

    claude mcp add chrome-devtools -- npx -y chrome-devtools-mcp@latest --browser-url http://127.0.0.1:9222

The browser itself is visible in router's VNC tab; add chrome-vnc:5900 there as a
target. Claude drives that same browser over CDP, so a login you complete by hand in
the VNC session is a login Claude then has.
MSG
