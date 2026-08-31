# Shared fail-closed resolver for the "bind to exactly one Docker network" pattern used
# by both wayvnc (VNC_BIND_ALIAS) and cdp-wrap (CDP_BIND_ALIAS).
#
# This container sits on two networks with deliberately different audiences - the CDP
# port must not be reachable from the VNC network and vice versa - so binding 0.0.0.0
# would silently undo the segmentation. Resolving a per-network compose alias gives the
# one address to bind, and failing (rather than falling back to 0.0.0.0) is the point:
# a silent fallback defeats the whole reason the variable exists.
#
# Echoes the resolved address on stdout; returns non-zero on failure.
resolve_bind_alias() {
  local alias_name="$1" resolved=""
  for _ in $(seq 1 25); do
    resolved="$(getent hosts "${alias_name}" 2>/dev/null | awk '{print $1; exit}')"
    [[ -n "${resolved}" ]] && break
    sleep 0.2
  done
  if [[ -z "${resolved}" ]]; then
    echo "resolve_bind_alias: '${alias_name}' did not resolve via 'getent hosts' - refusing to fall back to 0.0.0.0, which would expose this port on every attached network. Check that this container is actually attached to the network defining that alias." >&2
    return 1
  fi
  echo "${resolved}"
}
