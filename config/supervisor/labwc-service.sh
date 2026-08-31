#!/usr/bin/env bash
set -eu

. /etc/code-docker-chrome/wait-for-wayland.sh

# The post-start steps can only run once labwc has made its socket, but labwc must stay
# in the foreground so supervisord's stop signal reaches it through the exec below.
# A background subshell survives the exec as a normal child.
(
  wait_for_wayland_socket

  # labwc has no output-resolution directive of its own; wlr-randr is the generic
  # wlroots client for it. --custom-mode (not --mode) is required because the headless
  # backend only pre-registers 1280x720 and has no EDID mode list to pick from.
  wlr-randr --output HEADLESS-1 --custom-mode "${SCREEN_SIZE:-1920x1080}" \
    || echo "[labwc-service] WARNING: wlr-randr failed to set output mode" >&2

  # D-Bus activation environment is fixed when dbus-daemon starts and does not pick up
  # a later WAYLAND_DISPLAY export on its own. Without this, anything the portal
  # activates on demand never sees a Wayland display.
  dbus-update-activation-environment --systemd \
    WAYLAND_DISPLAY XDG_RUNTIME_DIR XDG_CURRENT_DESKTOP DBUS_SESSION_BUS_ADDRESS \
    2>/dev/null || true
) &

exec labwc
