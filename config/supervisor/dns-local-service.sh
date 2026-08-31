#!/usr/bin/env bash
set -eu

# This container's side of the shared dns-local program (the resolver itself, and the
# writeup of the bug it exists for, live in qwreey/router-docker-client's dns-local/,
# fetched at build time by the Dockerfile).
#
# Why it is needed: attached to code-docker this container sits on `internal: true`
# networks only, where Docker's embedded DNS (127.0.0.11) answers same-network names and
# returns an immediate definitive SERVFAIL for everything else. A browser with no
# external DNS is useless. Pointing at router alone doesn't work either, because both
# wayvnc and cdp-wrap resolve compose aliases with getent and fail closed - router's
# dnsmasq knows nothing about compose aliases. Both upstreams are genuinely required,
# which is exactly what the shared strict-order dnsmasq script sets up.
#
# Defaults to on, unlike roblox-studio-docker's copy: that project has a standalone mode
# with a working 127.0.0.11 and nothing to forward to. This one has no deployment
# without router.
export DNS_LOCAL_ENABLED="${DNS_LOCAL_ENABLED:-true}"
export NETSHARE_DIR="${NETSHARE_DIR:-/etc/code-docker-chrome/router-client/netshare}"

exec /etc/code-docker-chrome/router-client/dns-local/dns-local.sh
