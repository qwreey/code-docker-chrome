#!/bin/sh
# Mounted at /usr/local/bin/chrome-screen in code-docker. The client is cdp-bridge's
# screen mode, which install.sh builds onto the /code volume; it talks to cdp-unwrap's
# VNC port on loopback.
exec /code/.local/bin/cdp-bridge screen "$@"
