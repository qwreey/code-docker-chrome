#!/usr/bin/env bash
set -eu

. /etc/code-docker-chrome/resolve-bind-alias.sh

CDP_PORT="${CDP_PORT:-9222}"
CDP_WRAP_PORT="${CDP_WRAP_PORT:-9223}"
CDP_BIND_ALIAS="${CDP_BIND_ALIAS:-}"

# Bind to the code-docker-internal alias only, never 0.0.0.0 - the VNC network must not
# be able to reach CDP any more than code-docker can reach VNC. Fails closed; see
# resolve-bind-alias.sh.
if [[ -z "${CDP_BIND_ALIAS}" ]]; then
  echo "[cdp-wrap-service] FATAL: CDP_BIND_ALIAS is unset. This image is a code-docker provider and always sits on more than one network; binding every interface would expose full browser control on the VNC network too." >&2
  exit 1
fi
BIND_ADDR="$(resolve_bind_alias "${CDP_BIND_ALIAS}")"

echo "[cdp-wrap-service] listening on ${BIND_ADDR}:${CDP_WRAP_PORT} (resolved from ${CDP_BIND_ALIAS}) -> 127.0.0.1:${CDP_PORT}"
exec cdp-bridge wrap \
  -listen "${BIND_ADDR}:${CDP_WRAP_PORT}" \
  -upstream "127.0.0.1:${CDP_PORT}"
