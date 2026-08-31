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

# Built from the source the overlay mounts, not fetched. mise is here for the Go
# toolchain only, so the agent container needs nothing preinstalled, but the module
# itself deliberately does not come over the network:
#
#   - it would be a different build than the one running. `@latest` resolves against
#     this repo's default branch, while the wrap half is baked into the chrome image
#     from whatever checkout built it. The source under ${SRC_DIR} is that checkout,
#     so building it is the only way the two halves are guaranteed to match.
#   - mise's go backend resolves versions from tags and this repo has none, so
#     `mise use -g go:<this module>@latest` fails outright with "no versions found ...
#     matching date filter" (measured). Tagging releases would fix that, but a compose
#     provider is not a library and does not want a release cadence.
#   - and it works with no egress at all, which matters in a container whose whole
#     network policy is "the border is router's alone".
#
# ${BIN} is a fixed path on the volume because cdp-unwrap.conf names it literally -
# a supervisord unit should not depend on where a package manager put a shim today.
echo "==> installing cdp-bridge"
"${MISE}" ls --installed 2>/dev/null | grep -q '^go\b' || "${MISE}" use -g go@latest
( cd "${SRC_DIR}" && "${MISE}" exec -- env CGO_ENABLED=0 go build -ldflags="-s -w" -o "${BIN}" . )
echo "    built ${SRC_DIR} -> ${BIN}"
"${BIN}" 2>&1 | head -1 || true

echo "==> ensuring node is available for chrome-devtools-mcp"
"${MISE}" ls --installed 2>/dev/null | grep -q '^node\b' || "${MISE}" use -g node@lts

echo "==> installing the cdp-unwrap supervisord unit"
mkdir -p /var/log/cdp-unwrap
install -m 644 "$(dirname "$0")/cdp-unwrap.conf" "${UNIT_DIR}/cdp-unwrap.conf"

echo "==> reloading supervisord"
reload-services

cat <<'MSG'

Done. Check it reached Chrome:

    curl -s http://127.0.0.1:9222/json/version

Then register the MCP server (once per project, or -s user for all of them):

    claude mcp add chrome-devtools -- npx -y chrome-devtools-mcp@latest --browser-url http://127.0.0.1:9222

The browser itself is visible in router's VNC tab; add chrome-vnc:5900 there as a
target. Claude drives that same browser over CDP, so a login you complete by hand in
the VNC session is a login Claude then has.
MSG
