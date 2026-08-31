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

# A failed supervisorctl call (socket briefly unavailable, EPIPE, fork failure under
# memory pressure) yields an empty state, which is not the same statement as "the
# compositor died" - and acting on it would tear down a person's live VNC session over
# a transient. Only a state we actually read counts, and a run of unreadable ones has to
# persist before it is treated as failure.
consecutive_unreadable=0
MAX_UNREADABLE=5

while "${running}"; do
  for program in "${CRITICAL_PROGRAMS[@]}"; do
    state="$(supervisorctl -s "${SUPERVISOR_SOCK}" status "${program}" 2>/dev/null | awk '{print $2}')"
    if [[ -z "${state}" ]]; then
      consecutive_unreadable=$(( consecutive_unreadable + 1 ))
      if (( consecutive_unreadable >= MAX_UNREADABLE )); then
        echo "[critical-watchdog] supervisorctl unreadable ${consecutive_unreadable}x in a row - treating as a dead supervisord and shutting down"
        supervisorctl -s "${SUPERVISOR_SOCK}" shutdown >/dev/null 2>&1
        exit 0
      fi
      echo "[critical-watchdog] could not read ${program} state (${consecutive_unreadable}/${MAX_UNREADABLE}) - retrying"
      continue
    fi
    consecutive_unreadable=0
    if [[ "${state}" != "RUNNING" && "${state}" != "STARTING" ]]; then
      echo "[critical-watchdog] ${program} is not running (state=${state:-unknown}) - shutting the whole stack down"
      supervisorctl -s "${SUPERVISOR_SOCK}" shutdown >/dev/null 2>&1
      exit 0
    fi
  done
  sleep 2
done
exit 0
