#!/usr/bin/env bash
set -eu

. /etc/code-docker-chrome/wait-for-wayland.sh
wait_for_wayland_socket

CHROME_PROFILE_DIR="${CHROME_PROFILE_DIR:-/data/profile}"
CDP_PORT="${CDP_PORT:-9222}"
mkdir -p "${CHROME_PROFILE_DIR}"

# --no-sandbox, deliberately. Two independent reasons, both verified rather than assumed
# (see the research writeup this repo was built from):
#
#  1. This container runs as root, and Chrome refuses outright to start its zygote
#     sandbox as root - "Running as root without --no-sandbox is not supported"
#     (crbug.com/638180). Relaxing seccomp does not change that; it is a uid check.
#  2. Docker's default seccomp profile blocks clone(CLONE_NEWUSER), so even as a
#     non-root user the namespace sandbox fails with "No usable sandbox!" unless the
#     profile is relaxed or CAP_SYS_ADMIN granted - and under a nested LXC host that
#     may not be obtainable at all.
#
# This is what linuxserver/docker-chromium (the closest comparable image: desktop +
# labwc + VNC) ships too, hardcoded in its own wrapper. The container is the boundary
# here; keeping the browser reachable only over an authenticated CDP port and an
# isolated VNC network is where the actual containment lives.
#
# The debugging port stays on loopback on purpose - Chrome ignores
# --remote-debugging-address=0.0.0.0 (measured), and CDP has no authentication of its
# own, so cdp-wrap in front of it is the only thing that should ever be listening on a
# network. Chrome 136+ also refuses --remote-debugging-port without an explicit
# --user-data-dir, which the profile dir above satisfies.
exec chromium \
  --no-sandbox \
  --ozone-platform=wayland \
  --user-data-dir="${CHROME_PROFILE_DIR}" \
  --remote-debugging-port="${CDP_PORT}" \
  --password-store=basic \
  --no-first-run \
  --no-default-browser-check \
  --start-maximized \
  --disable-features=Translate \
  ${CHROME_EXTRA_ARGS:-} \
  "${CHROME_START_URL:-about:blank}"
