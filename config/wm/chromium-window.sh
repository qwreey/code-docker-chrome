#!/bin/sh
# Opens a window in the Chrome supervisord runs, for the launcher, the desktop menu and
# xdg-open. That Chrome is the only one allowed to own the profile: it carries the CDP
# port and the localhost mapping, and a second Chrome on the same profile would take the
# profile lock and leave supervisord's restart of the real one refusing to start.
#
# So this only ever hands its arguments to the running instance - Chromium's process
# singleton does that and exits - never starts one itself. supervisord restarts Chrome
# within a second or two of its last window closing; wait that out instead.
#
# --no-sandbox and --ozone-platform=wayland are needed even though this process only
# forwards: Chromium refuses to start as root without the first, and falls back to X11
# (and dies, there is no X display) without the second, before it gets as far as
# looking for the running instance.
for _ in 1 2 3 4 5 6 7 8 9 10; do
  if pgrep -x chromium >/dev/null 2>&1; then
    exec chromium --user-data-dir="${CHROME_PROFILE_DIR:-/data/profile}" \
      --no-sandbox --ozone-platform=wayland "$@"
  fi
  sleep 1
done
echo "chromium-window: supervisord's Chrome is not running (supervisorctl status chromium)" >&2
exit 1
