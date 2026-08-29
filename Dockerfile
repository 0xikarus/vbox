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
    && rm -rf /var/lib/apt/lists/*

ARG VMBOX_COMPONENTS=codex,claude,bun,foundry
RUN set -eux; \
    packages=""; \
    case ",$VMBOX_COMPONENTS," in *,codex,*) packages="$packages @openai/codex" ;; esac; \
    case ",$VMBOX_COMPONENTS," in *,claude,*) packages="$packages @anthropic-ai/claude-code" ;; esac; \
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
RUN chmod 0755 /usr/local/bin/vmbox-entrypoint

ENV HOME=/data/home
WORKDIR /data/workspace

ENTRYPOINT ["/usr/local/bin/vmbox-entrypoint"]
