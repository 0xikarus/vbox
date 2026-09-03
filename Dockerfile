FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/vmbox-runtime ./cmd/vmbox-runtime
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/vmbox-controller ./cmd/vmbox-controller

FROM node:22-bookworm-slim

ARG VMBOX_IMAGE_VERSION=dev

ENV DEBIAN_FRONTEND=noninteractive
ENV FOUNDRY_DIR=/opt/foundry
ENV BUN_INSTALL=/opt/bun
ENV PATH=/opt/bun/bin:/opt/foundry/bin:${PATH}
ENV LANG=C.UTF-8
ENV LC_ALL=C.UTF-8
ENV COLORTERM=truecolor

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
      sudo \
      unzip \
      tmux \
      util-linux \
    && rm -rf /var/lib/apt/lists/*

RUN groupadd --gid 10001 vmbox \
    && useradd --uid 10001 --gid vmbox --home-dir /data/home --shell /bin/bash --no-create-home vmbox \
    && printf '%s\n' 'vmbox ALL=(ALL) NOPASSWD:ALL' >/etc/sudoers.d/vmbox \
    && chmod 0440 /etc/sudoers.d/vmbox \
    && mkdir -p /data/home /data/workspace /data/.vmbox \
    && chown -R vmbox:vmbox /data

ARG VMBOX_COMPONENTS=codex,claude,opencode,bun,foundry
LABEL org.opencontainers.image.version=$VMBOX_IMAGE_VERSION \
      io.vmbox.image.version=$VMBOX_IMAGE_VERSION \
      io.vmbox.components=$VMBOX_COMPONENTS
RUN set -eux; \
    packages="@railway/cli"; \
    case ",$VMBOX_COMPONENTS," in *,codex,*) packages="$packages @openai/codex" ;; esac; \
    case ",$VMBOX_COMPONENTS," in *,claude,*) packages="$packages @anthropic-ai/claude-code" ;; esac; \
    case ",$VMBOX_COMPONENTS," in *,opencode,*) packages="$packages opencode-ai" ;; esac; \
    if [ -n "$packages" ]; then npm install --global $packages; fi

RUN set -eux; \
    mkdir -p /opt/bun; \
    case ",$VMBOX_COMPONENTS," in \
      *,bun,*) curl -fsSL https://bun.sh/install | bash ;; \
    esac

RUN set -eux; \
    mkdir -p /opt/foundry/bin; \
    case ",$VMBOX_COMPONENTS," in \
      *,foundry,*) curl -fsSL https://foundry.paradigm.xyz | bash; /opt/foundry/bin/foundryup ;; \
    esac

COPY tmux.conf /etc/vmbox/tmux.conf
COPY entrypoint.sh /usr/local/bin/vmbox-entrypoint
COPY --from=build /out/vmbox-runtime /usr/local/bin/vmbox-runtime
COPY --from=build /out/vmbox-controller /usr/local/bin/vmbox-controller
RUN set -eux; \
    normalized_components="$(printf '%s' "$VMBOX_COMPONENTS" | tr ',' '\n' | sed '/^$/d' | sort -u | paste -sd, -)"; \
    printf '%s\n' "$normalized_components" >/usr/local/lib/vmbox-bootstrap-components; \
    printf '%s\n' "$VMBOX_IMAGE_VERSION" >/usr/local/lib/vmbox-image-version; \
    { \
      printf 'image-version=%s\ncomponents=%s\n' "$VMBOX_IMAGE_VERSION" "$normalized_components"; \
      git --version; gh --version | sed -n '1p'; tmux -V; node --version; \
      bun --version; codex --version; claude --version; opencode --version; forge --version | sed -n '1p'; \
      sha256sum /usr/local/bin/vmbox-runtime /usr/local/bin/vmbox-entrypoint /etc/vmbox/tmux.conf; \
    } >/usr/local/lib/vmbox-image-manifest; \
    sha256sum /usr/local/lib/vmbox-image-manifest | sed 's/[[:space:]].*$//' >/usr/local/lib/vmbox-component-fingerprint; \
    mkdir -p /etc/vmbox; \
    chmod 0755 /usr/local/bin/vmbox-entrypoint /usr/local/bin/vmbox-runtime /usr/local/bin/vmbox-controller; \
    chmod 0644 /usr/local/lib/vmbox-bootstrap-components /usr/local/lib/vmbox-image-version /usr/local/lib/vmbox-image-manifest /usr/local/lib/vmbox-component-fingerprint; \
    ln -s vmbox-runtime /usr/local/bin/vmbox-report; \
    ln -s vmbox-runtime /usr/local/bin/vmbox-finish; \
    ln -s vmbox-runtime /usr/local/bin/vmbox-ask

ENV HOME=/data/home
WORKDIR /data/workspace

HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=6 CMD ["/usr/local/bin/vmbox-runtime", "health"]
ENTRYPOINT ["/usr/local/bin/vmbox-entrypoint"]
