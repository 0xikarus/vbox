FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/vmbox-runtime ./cmd/vmbox-runtime

FROM node:22-bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive
ENV FOUNDRY_DIR=/opt/foundry
ENV BUN_INSTALL=/opt/bun
ENV PATH=/opt/bun/bin:/opt/foundry/bin:${PATH}

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
RUN set -eux; \
    packages=""; \
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

COPY entrypoint.sh /usr/local/bin/vmbox-entrypoint
COPY --from=build /out/vmbox-runtime /usr/local/bin/vmbox-runtime
RUN chmod 0755 /usr/local/bin/vmbox-entrypoint /usr/local/bin/vmbox-runtime \
    && ln -s vmbox-runtime /usr/local/bin/vmbox-report \
    && ln -s vmbox-runtime /usr/local/bin/vmbox-finish \
    && ln -s vmbox-runtime /usr/local/bin/vmbox-ask

ENV HOME=/data/home
WORKDIR /data/workspace

ENTRYPOINT ["/usr/local/bin/vmbox-entrypoint"]
