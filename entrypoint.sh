#!/usr/bin/env bash
set -euo pipefail

# Everything past the exec at the bottom is supervisord's job (config/supervisord.conf
# + config/supervisord.d/*.conf). This script only does what must happen once, before
# any program starts.
#
# Unlike roblox-studio-docker's equivalent, there is no standalone mode to keep working
# here: this image exists only as a code-docker provider (see README), so NETINIT_WAIT
# and DNS_LOCAL_ENABLED default to on rather than off. There is no deployment of this
# container without a router in front of it.

export HOME="${HOME:-/root}"
export XDG_RUNTIME_DIR="/tmp/xdg-runtime"

# `docker restart` reuses the writable layer, so a stale wayland-N socket from a
# previously crashed labwc survives here and would satisfy wait-for-wayland.sh's
# existence check instantly - before the new labwc has made its own socket - leaving
# wlr-randr/wayvnc dialing a dead server forever. Wipe it so only a live socket is
# findable.
rm -rf "${XDG_RUNTIME_DIR}"
mkdir -p "${XDG_RUNTIME_DIR}"
chmod 700 "${XDG_RUNTIME_DIR}"

# Block until code-docker's netinit-docker (host-side agent, matched by the
# netinit.provider label in code-docker-chrome.yml) has planted this container's
# default route. That agent necessarily runs *after* the container starts, so without
# this wait Chrome could be up and browsing before the egress policy exists - and a
# browser is precisely the thing the router boundary is there to constrain.
#
# Fail-closed: exit non-zero so `restart: unless-stopped` retries, rather than
# continuing unrouted. Reading `ip route` needs no capability; only the provider needs
# NET_ADMIN, and it deliberately lives outside this container.
if [[ "${NETINIT_WAIT:-true}" == "true" ]]; then
	timeout_s="${NETINIT_WAIT_TIMEOUT:-60}"
	waited=0
	while ! ip route show default 2>/dev/null | grep -q .; do
		if (( waited >= timeout_s )); then
			echo >&2 "entrypoint: no default route after ${timeout_s}s - the netinit provider never planted one. Refusing to start Chrome unrouted; exiting so restart: unless-stopped retries."
			exit 1
		fi
		if (( waited == 0 )); then
			echo "entrypoint: waiting for the netinit provider to plant a default route..."
		fi
		sleep 2
		waited=$(( waited + 2 ))
	done
	echo "entrypoint: default route present ($(ip route show default | head -1)) - continuing"
fi

export WLR_BACKENDS=headless
export WLR_LIBINPUT_NO_DEVICES=1
export WLR_RENDERER="${WLR_RENDERER:-pixman}"

# No real desktop session here, so xdg-desktop-portal has no backend to auto-select
# via portals.conf; this makes it fall back to matching installed .portal files'
# deprecated UseIn= key. Same reasoning as roblox-studio-docker's entrypoint.
export XDG_CURRENT_DESKTOP=GNOME

# Fixed rather than discovered - the bus socket path depends only on XDG_RUNTIME_DIR,
# so it can be exported once and inherited by every supervisord child.
export DBUS_SESSION_BUS_ADDRESS="unix:path=${XDG_RUNTIME_DIR}/bus"

mkdir -p "${CHROME_PROFILE_DIR:-/data/profile}"

echo "[entrypoint] handing off to supervisord"
exec supervisord -n -c /etc/code-docker-chrome/supervisord.conf
