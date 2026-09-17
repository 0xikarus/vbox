FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/vmbox-runtime ./cmd/vmbox-runtime
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/vmbox-controller ./cmd/vmbox-controller
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/vmbox-worker-agent ./cmd/vmbox-worker-agent
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/vmbox-shared-worker ./cmd/vmbox-shared-worker

FROM node:22-bookworm-slim

COPY --from=ghcr.io/astral-sh/uv:0.12.15@sha256:62f8c047d0a0e9ece6b53fc63df902585a67a47a7f318ddec4a37db586edc8e3 /uv /uvx /usr/local/bin/

ARG VMBOX_IMAGE_VERSION=dev

ENV DEBIAN_FRONTEND=noninteractive
ENV FOUNDRY_DIR=/opt/foundry
ENV BUN_INSTALL=/opt/bun
ENV PATH=/opt/bun/bin:/opt/foundry/bin:${PATH}
ENV LANG=C.UTF-8
ENV LC_ALL=C.UTF-8
ENV COLORTERM=truecolor
# Each worker is a dedicated agent box. Hosts allowing Chromium namespaces can
# set this to false to enable Chromium's additional process sandbox.
ENV VMBOX_CHROMIUM_NO_SANDBOX=true

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
      bash \
      bubblewrap \
      ca-certificates \
      curl \
      git \
      gh \
      jq \
      openssh-client \
      locales \
      ncurses-term \
      python3 \
      python-is-python3 \
      python3-pip \
      python3-venv \
      pipx \
      sudo \
      tini \
      unzip \
      tmux \
      util-linux \
    && rm -rf /var/lib/apt/lists/*

RUN groupadd --gid 10001 vmbox \
    && useradd --uid 10001 --gid vmbox --home-dir /data/home --shell /bin/bash --no-create-home vmbox \
    && printf '%s\n' 'vmbox ALL=(ALL) NOPASSWD:ALL' >/etc/sudoers.d/vmbox \
    && chmod 0440 /etc/sudoers.d/vmbox \
    && mkdir -p /data/home /data/workspace /data/.vmbox \
    && chmod 0700 /data/home \
    && chown -R vmbox:vmbox /data

# Desktop is included by default; operators can still build a shell-only image.
ARG VMBOX_DESKTOP=true
RUN if [ "$VMBOX_DESKTOP" = true ]; then \
      apt-get update && apt-get install -y --no-install-recommends \
        tigervnc-standalone-server openbox chromium chromium-sandbox xdotool xprintidle xterm dbus-x11 fonts-dejavu-core tint2 pcmanfm xdg-user-dirs adwaita-icon-theme \
      && rm -rf /var/lib/apt/lists/*; \
    fi

ARG VMBOX_COMPONENTS=codex,claude,opencode,bun,foundry
RUN apt-get update && apt-get install -y --no-install-recommends \
      xz-utils libxxf86vm1 libxfixes3 libxi6 libxrender1 libxkbcommon0 libgl1 libsm6 libice6 \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /opt/vmbox/blender-5.1.2 \
    && curl -fsSL https://download.blender.org/release/Blender5.1/blender-5.1.2-linux-x64.tar.xz -o /tmp/blender.tar.xz \
    && echo 'aaccb355f50183979b698bcce7467103a76261b5fa59f4972295842662a285fb  /tmp/blender.tar.xz' | sha256sum -c - \
    && tar -xJf /tmp/blender.tar.xz --strip-components=1 -C /opt/vmbox/blender-5.1.2 \
    && rm /tmp/blender.tar.xz \
    && /opt/vmbox/blender-5.1.2/blender --version | head -1 | grep -Fx 'Blender 5.1.2' \
    && python3 -m venv /opt/vmbox/blender-mcp-1.9.1 \
    && /opt/vmbox/blender-mcp-1.9.1/bin/pip install --no-cache-dir blender-mcp==1.9.1
LABEL org.opencontainers.image.version=$VMBOX_IMAGE_VERSION \
      io.vmbox.image.version=$VMBOX_IMAGE_VERSION \
      io.vmbox.components=$VMBOX_COMPONENTS
# Keep every distributable layer credential-free. Installers and version checks
# may create caches under root; runtime credentials are synchronized only to /data.
RUN set -eux; \
    packages="@railway/cli"; \
    case ",$VMBOX_COMPONENTS," in *,codex,*) packages="$packages @openai/codex" ;; esac; \
    case ",$VMBOX_COMPONENTS," in *,claude,*) packages="$packages @anthropic-ai/claude-code" ;; esac; \
    case ",$VMBOX_COMPONENTS," in *,opencode,*) packages="$packages opencode-ai" ;; esac; \
    if [ -n "$packages" ]; then npm install --global --foreground-scripts $packages; fi; \
    rm -rf /root/.cache /root/.claude /root/.codex /root/.config /root/.docker /root/.npm /root/.ssh /tmp/* /var/tmp/*

RUN set -eux; \
    mkdir -p /opt/bun; \
    case ",$VMBOX_COMPONENTS," in \
      *,bun,*) curl -fsSL https://bun.sh/install | bash ;; \
    esac; \
    rm -rf /root/.cache /root/.config /root/.npm /root/.ssh /tmp/* /var/tmp/*

RUN set -eux; \
    mkdir -p /opt/foundry/bin; \
    case ",$VMBOX_COMPONENTS," in \
      *,foundry,*) curl -fsSL https://foundry.paradigm.xyz | bash; /opt/foundry/bin/foundryup ;; \
    esac; \
    rm -rf /root/.cache /root/.config /root/.foundry /root/.ssh /tmp/* /var/tmp/*

COPY tmux.conf /etc/vmbox/tmux.conf
COPY entrypoint.sh /usr/local/bin/vmbox-entrypoint
COPY --from=build /out/vmbox-runtime /usr/local/bin/vmbox-runtime
COPY --from=build /out/vmbox-controller /usr/local/bin/vmbox-controller
COPY --from=build /out/vmbox-worker-agent /usr/local/bin/vmbox-worker-agent
COPY --from=build /out/vmbox-shared-worker /usr/local/bin/vmbox-shared-worker
RUN set -eux; \
    normalized_components="$(printf '%s' "$VMBOX_COMPONENTS" | tr ',' '\n' | sed '/^$/d' | sort -u | paste -sd, -)"; \
    printf '%s\n' "$normalized_components" >/usr/local/lib/vmbox-bootstrap-components; \
    printf '%s\n' "$VMBOX_IMAGE_VERSION" >/usr/local/lib/vmbox-image-version; \
    { \
      printf 'image-version=%s\ncomponents=%s\n' "$VMBOX_IMAGE_VERSION" "$normalized_components"; \
      git --version; gh --version | sed -n '1p'; railway --version; tmux -V; node --version; \
      npm --version; python --version; python3 --version; python3 -m pip --version; pipx --version; uv --version; uvx --version; \
      bun --version; codex --version; claude --version; opencode --version; forge --version | sed -n '1p'; \
      sha256sum /usr/local/bin/vmbox-runtime /usr/local/bin/vmbox-worker-agent /usr/local/bin/vmbox-entrypoint /etc/vmbox/tmux.conf; \
    } >/usr/local/lib/vmbox-image-manifest; \
    sha256sum /usr/local/lib/vmbox-image-manifest | sed 's/[[:space:]].*$//' >/usr/local/lib/vmbox-component-fingerprint; \
    mkdir -p /etc/vmbox; \
    chmod 0755 /usr/local/bin/vmbox-entrypoint /usr/local/bin/vmbox-runtime /usr/local/bin/vmbox-controller; \
    chmod 0644 /usr/local/lib/vmbox-bootstrap-components /usr/local/lib/vmbox-image-version /usr/local/lib/vmbox-image-manifest /usr/local/lib/vmbox-component-fingerprint; \
    ln -s vmbox-runtime /usr/local/bin/vmbox-report; \
    ln -s vmbox-runtime /usr/local/bin/vmbox-finish; \
    ln -s vmbox-runtime /usr/local/bin/vmbox-ask; \
    rm -rf /root/.cache /root/.claude /root/.codex /root/.config /root/.docker /root/.npm /root/.ssh /tmp/* /var/tmp/*
ENV HOME=/data/home
WORKDIR /data/workspace

HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=6 CMD ["/usr/local/bin/vmbox-runtime", "health"]
# tini is PID 1 so orphaned processes are reaped. Without a reaping init the
# entrypoint's `exec sudo` leaves sudo as PID 1, which waits only for its own
# child: every re-parented desktop/agent process stays a zombie and consumes a
# cgroup pid slot until `pids.max` is exhausted and nothing can fork.
ENTRYPOINT ["/usr/bin/tini", "-s", "--", "/usr/local/bin/vmbox-entrypoint"]
