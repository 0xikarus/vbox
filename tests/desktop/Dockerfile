# Build with a freshly compiled vmbox-runtime in the build context.
ARG VMBOX_TEST_BASE=ghcr.io/0xikarus/vmbox-service:desktop-d3a0d82
FROM ${VMBOX_TEST_BASE}
USER root
RUN apt-get update && apt-get install -y --no-install-recommends chromium chromium-sandbox xprintidle xdotool tint2 pcmanfm xdg-user-dirs && rm -rf /var/lib/apt/lists/*
COPY vmbox-runtime /usr/local/bin/vmbox-runtime
USER 10001:10001
ENV HOME=/data/home
ENV VMBOX_CHROMIUM_NO_SANDBOX=true
ENTRYPOINT ["sleep", "infinity"]
