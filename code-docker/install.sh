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
MODULE="github.com/qwreey/code-docker-chrome/cdp-bridge"
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

# cdp-unwrap.conf hardcodes ${BIN} rather than resolving whatever mise happens to do,
# because mise's go backend puts its shim under its own share directory - a path that
# depends on mise's layout and on the tool still being installed. A supervisord unit
# that outlives a `mise prune` is worth the one extra indirection, so whichever branch
# below runs, it lands the binary at exactly ${BIN}.
echo "==> installing cdp-bridge"
if "${MISE}" use -g "go:${MODULE}@latest" 2>/dev/null; then
  SHIM="$("${MISE}" which cdp-bridge 2>/dev/null || true)"
  if [[ -z "${SHIM}" ]]; then
    echo >&2 "install.sh: mise installed go:${MODULE} but 'mise which cdp-bridge' found nothing."
    exit 1
  fi
  ln -sf "${SHIM}" "${BIN}"
  echo "    via mise -> ${SHIM}"
else
  # Expected until this repo is published somewhere `go install` can reach. The source
  # is sitting right next to this script either way, so build it - mise supplies the Go
  # toolchain so the agent container needs nothing preinstalled.
  echo "    go:${MODULE} not fetchable - building from ${SRC_DIR}"
  "${MISE}" ls --installed 2>/dev/null | grep -q '^go\b' || "${MISE}" use -g go@latest
  ( cd "${SRC_DIR}" && "${MISE}" exec -- env CGO_ENABLED=0 go build -ldflags="-s -w" -o "${BIN}" . )
  echo "    built -> ${BIN}"
fi
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
