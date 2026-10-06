#!/bin/sh
# Mounted at /usr/local/bin/chrome-ports in code-docker. The CLI itself comes in with the
# directory mount at /run/code-docker-chrome/code-docker, so a pull of this repo reaches
# code-docker without recreating it - a file bind mount keeps the replaced file.
exec python3 /run/code-docker-chrome/code-docker/chrome-ports "$@"
