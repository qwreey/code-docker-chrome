# Sourced, not exec'd, by every service that needs labwc's Wayland socket - supervisord
# runs each program as its own process with no shared state, so labwc-service.sh's own
# post-start step and chromium/wayvnc each need this independently.
#
# Globs for any wayland-* rather than hardcoding wayland-0 so swapping compositors
# doesn't break it. entrypoint.sh wipes $XDG_RUNTIME_DIR before supervisord starts, so
# a stale socket from a crashed previous run can't satisfy this instantly.
wait_for_wayland_socket() {
  local candidate socket
  for _ in $(seq 1 50); do
    candidate="$(find "${XDG_RUNTIME_DIR}" -maxdepth 1 -name 'wayland-*' ! -name '*.lock' 2>/dev/null | head -n1)"
    if [[ -n "${candidate}" ]]; then
      socket="$(basename "${candidate}")"
      export WAYLAND_DISPLAY="${socket}"
      echo "[wait-for-wayland] WAYLAND_DISPLAY=${WAYLAND_DISPLAY}"
      return 0
    fi
    sleep 0.2
  done
  echo "[wait-for-wayland] labwc failed to create a Wayland socket after 10s" >&2
  return 1
}
