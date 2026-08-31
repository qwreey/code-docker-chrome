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
# neither is specific to this project.
ADD https://github.com/qwreey/router-docker-client.git#main:dns-local /etc/code-docker-chrome/router-client/dns-local
ADD https://github.com/qwreey/router-docker-client.git#main:netshare /etc/code-docker-chrome/router-client/netshare
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
# Copied anywhere else the file is inert - which is how the decoration-disabling rule
# below silently did nothing. roblox-studio-docker lands its labwc config the same way.
COPY config/wm/labwc-rc.xml /etc/xdg/labwc/rc.xml
COPY config/wm/labwc-autostart /etc/xdg/labwc/autostart
COPY entrypoint.sh /etc/code-docker-chrome/entrypoint.sh
RUN chmod +x /etc/code-docker-chrome/entrypoint.sh /etc/code-docker-chrome/*-service.sh

ENV HOME=/root \
    XDG_CONFIG_HOME=/root/.config \
    CHROME_PROFILE_DIR=/data/profile

ENTRYPOINT ["/etc/code-docker-chrome/entrypoint.sh"]
