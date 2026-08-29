FROM node:22-bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive
ENV FOUNDRY_DIR=/opt/foundry
ENV PATH=/opt/foundry/bin:${PATH}

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
      tmux \
    && rm -rf /var/lib/apt/lists/*

RUN npm install --global @openai/codex @anthropic-ai/claude-code

RUN curl -fsSL https://foundry.paradigm.xyz | bash \
    && /opt/foundry/bin/foundryup

COPY entrypoint.sh /usr/local/bin/vmbox-entrypoint
RUN chmod 0755 /usr/local/bin/vmbox-entrypoint

ENV HOME=/data/home
WORKDIR /data/workspace

ENTRYPOINT ["/usr/local/bin/vmbox-entrypoint"]
