# cdp-bridge is a static Go binary used on both sides of the boundary: `wrap` runs here
# next to Chrome, `unwrap` runs inside code-docker. Built here so the image is
# self-contained, but the same module is what code-docker/install.sh installs over there
# (see that script - it prefers mise so the agent container needs no Go toolchain).
FROM golang:1.27-alpine AS cdp-bridge
WORKDIR /src
COPY cdp-bridge/go.mod ./
COPY cdp-bridge/main.go ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /cdp-bridge .

FROM archlinux

# Arch partial upgrades break glibc-linked binaries (chromium fails with
# "GLIBC_x.y not found" if installed with -Sy rather than -Syu) - always -Syu here.
RUN pacman -Syu --noconfirm --needed \
      chromium \
      labwc \
      wlr-randr \
      wayvnc \
      waybar \
      wofi \
      thunar \
      desktop-file-utils \
      dbus \
      supervisor \
      dnsmasq \
      bind \
      bash \
      procps-ng \
      iproute2 \
      curl \
      openssl \
      openssh \
      mesa \
      libxkbcommon \
      wayland \
      xdg-utils \
      xdg-desktop-portal \
      xdg-desktop-portal-gtk \
      ttf-dejavu \
      noto-fonts \
      noto-fonts-cjk \
      noto-fonts-emoji \
      hicolor-icon-theme \
    && pacman -Scc --noconfirm \
    && rm -rf /var/cache/pacman/pkg/*

# Per-program rotated log dirs, matching the stdout_logfile paths in supervisord.d/.
RUN mkdir -p /var/log/dbus /var/log/labwc /var/log/wayvnc /var/log/chromium \
      /var/log/cdp-wrap /var/log/critical-watchdog /var/log/dns-local

# dns-local is what gives this container working DNS while it sits on internal: true
# networks; netshare carries the wait-until helper it uses. Both are fetched from
# router-docker-client rather than vendored, exactly as roblox-studio-docker does -
# neither is specific to this project. Pinned to that repo's release tag (see its own
# CLAUDE.md); code-docker's dev-bump-router-client.sh moves this default.
ARG ROUTER_CLIENT_REF=v0.1.0
ADD https://github.com/qwreey/router-docker-client.git#${ROUTER_CLIENT_REF}:dns-local /etc/code-docker-chrome/router-client/dns-local
ADD https://github.com/qwreey/router-docker-client.git#${ROUTER_CLIENT_REF}:netshare /etc/code-docker-chrome/router-client/netshare
RUN chmod +x /etc/code-docker-chrome/router-client/dns-local/dns-local.sh

COPY --from=cdp-bridge /cdp-bridge /usr/local/bin/cdp-bridge

# The config tree flattens into /etc/code-docker-chrome/ the same way code-docker's own
# `COPY config ... /etc/code-docker/` does, so service scripts reference stable paths
# with no per-file Dockerfile wiring.
COPY config/supervisord.conf /etc/code-docker-chrome/supervisord.conf
COPY config/supervisord.d/ /etc/code-docker-chrome/supervisord.d/
COPY config/supervisor/ /etc/code-docker-chrome/
# /etc/xdg/labwc/ specifically: labwc only reads $XDG_CONFIG_HOME/labwc/,
# $HOME/.config/labwc/ and /etc/xdg/labwc/, and labwc-service.sh execs it with no -C.
# Copied anywhere else the file is inert. roblox-studio-docker lands its labwc config
# the same way.
COPY config/wm/labwc-rc.xml /etc/xdg/labwc/rc.xml
COPY config/wm/labwc-menu.xml /etc/xdg/labwc/menu.xml
COPY config/wm/labwc-autostart /etc/xdg/labwc/autostart
COPY config/wm/waybar-config.jsonc /etc/xdg/labwc/waybar-config.jsonc
COPY config/wm/waybar-style.css /etc/xdg/labwc/waybar-style.css
COPY config/wm/wofi-toggle.sh /etc/xdg/labwc/wofi-toggle.sh
COPY config/wm/chromium-window.sh /usr/local/bin/chromium-window
RUN chmod +x /etc/xdg/labwc/autostart /etc/xdg/labwc/wofi-toggle.sh /usr/local/bin/chromium-window

# The launcher (wofi's drun mode) lists every .desktop entry. Ours replaces the stock
# chromium.desktop, which starts a second Chrome on a different profile and without
# --no-sandbox, so it dies at once as root. Everything else is hidden too: the packages
# above bring in entries like avahi-discover, qv4l2 and pinentry-qt that are noise here.
# NoDisplay only hides; MIME associations still work.
RUN printf '[Desktop Entry]\nVersion=1.0\nName=Chromium\nGenericName=New window\nExec=chromium-window %%U\nTerminal=false\nIcon=chromium\nType=Application\nCategories=Network;WebBrowser;\nMimeType=text/html;x-scheme-handler/http;x-scheme-handler/https;\n' \
      > /usr/share/applications/code-docker-chrome.desktop \
    && for f in /usr/share/applications/*.desktop; do \
         case "${f##*/}" in code-docker-chrome.desktop|thunar.desktop) continue ;; esac; \
         grep -q '^NoDisplay=true' "$f" || sed -i '0,/^\[Desktop Entry\]$/s//&\nNoDisplay=true/' "$f"; \
       done \
    && test "$(grep -L '^NoDisplay=true' /usr/share/applications/*.desktop | xargs -n1 basename | sort | tr '\n' ' ')" = "code-docker-chrome.desktop thunar.desktop " \
    && update-desktop-database /usr/share/applications

# Thunar is the file manager - Chrome's "Show in folder" asks D-Bus for
# org.freedesktop.FileManager1, which Thunar provides - and opens directories.
# Links and HTML files opened from Thunar go back to the running Chrome. Written to the
# system-wide /etc/xdg/mimeapps.list rather than with `xdg-mime default`, which writes
# under $HOME/.config and fails when that doesn't exist yet.
RUN printf '[Default Applications]\ninode/directory=thunar.desktop\ntext/html=code-docker-chrome.desktop\nx-scheme-handler/http=code-docker-chrome.desktop\nx-scheme-handler/https=code-docker-chrome.desktop\n' \
      > /etc/xdg/mimeapps.list \
    && test "$(grep -lx 'Name=org.freedesktop.FileManager1' /usr/share/dbus-1/services/*.service)" \
         = /usr/share/dbus-1/services/org.xfce.Thunar.FileManager1.service
# Titlebar buttons for Chromium's own titlebar and Thunar's. GNOME's default layout is
# close-only, and with XDG_CURRENT_DESKTOP=GNOME (entrypoint.sh) GTK takes it from the
# portal's GSettings rather than from gtk-3.0/settings.ini, so the schema default is
# what has to change. Minimize matters most: the taskbar is how a window comes back.
RUN printf "[org.gnome.desktop.wm.preferences]\nbutton-layout='appmenu:minimize,maximize,close'\n" \
      > /usr/share/glib-2.0/schemas/90_code-docker-chrome.gschema.override \
    && glib-compile-schemas /usr/share/glib-2.0/schemas \
    && test "$(gsettings get org.gnome.desktop.wm.preferences button-layout)" = "'appmenu:minimize,maximize,close'"

# chromium-service.sh's --host-resolver-rules is on Chromium's list of flags it warns
# about, as an infobar on every window that survives being dismissed only until the next
# one. The flag is the design here (see that script), so the warning is noise. Policy is
# the only switch for it; the cost is a "Managed by your organization" line in Chrome's
# menu.
RUN mkdir -p /etc/chromium/policies/managed \
    && printf '{"CommandLineFlagSecurityWarningsEnabled": false}\n' \
      > /etc/chromium/policies/managed/code-docker-chrome.json

COPY entrypoint.sh /etc/code-docker-chrome/entrypoint.sh
RUN chmod +x /etc/code-docker-chrome/entrypoint.sh /etc/code-docker-chrome/*-service.sh

ENV HOME=/root \
    XDG_CONFIG_HOME=/root/.config \
    CHROME_PROFILE_DIR=/data/profile

ENTRYPOINT ["/etc/code-docker-chrome/entrypoint.sh"]
