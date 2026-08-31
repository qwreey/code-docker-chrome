#!/usr/bin/env bash
set -eu
# Session bus at the fixed path entrypoint.sh already exported as
# DBUS_SESSION_BUS_ADDRESS. --nofork keeps it in the foreground so supervisord's stop
# signal reaches the daemon itself.
exec dbus-daemon --session --nofork --nopidfile \
  --address="unix:path=${XDG_RUNTIME_DIR}/bus"
