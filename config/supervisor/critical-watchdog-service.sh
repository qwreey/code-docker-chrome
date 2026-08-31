#!/usr/bin/env bash
set -u

# supervisord has no "tear the stack down if program X exits" directive, so this polls
# for it. dbus/labwc/wayvnc set autorestart=false + startretries=0 precisely so a first
# failure surfaces here instead of being retried into a crash loop.
#
# chromium is deliberately absent from this list - see chromium.conf. It is the payload,
# not the platform, and a person closing it from the VNC session is normal.
SUPERVISOR_SOCK="unix:///run/supervisor.sock"
CRITICAL_PROGRAMS=(dbus labwc wayvnc)

running=true
trap 'running=false' TERM INT

# Grace period: immediately after supervisord starts these may still read STOPPED before
# it gets around to spawning them, which would trip a false shutdown.
sleep 5

while "${running}"; do
  for program in "${CRITICAL_PROGRAMS[@]}"; do
    state="$(supervisorctl -s "${SUPERVISOR_SOCK}" status "${program}" 2>/dev/null | awk '{print $2}')"
    if [[ "${state}" != "RUNNING" && "${state}" != "STARTING" ]]; then
      echo "[critical-watchdog] ${program} is not running (state=${state:-unknown}) - shutting the whole stack down"
      supervisorctl -s "${SUPERVISOR_SOCK}" shutdown >/dev/null 2>&1
      exit 0
    fi
  done
  sleep 2
done
exit 0
