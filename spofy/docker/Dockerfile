# Spofy remnanode image: stock remnawave/node with the Spofy Xray build swapped in.
# Only /usr/local/bin/xray changes (rw-core is a symlink to it); rw-node itself is untouched.
ARG BASE=remnawave/node:3.4.5@sha256:8f5a41708dfa86acc2561a520c9b40e2ff30812a16c28cf6bba6c365ca0f7d65
FROM ${BASE}
ARG XRAY_REV
COPY xray /usr/local/bin/xray
RUN chmod 0755 /usr/local/bin/xray && /usr/local/bin/xray version | head -1 | grep -q "spofy-" \
    && ls -l /usr/local/bin/rw-core
LABEL org.opencontainers.image.title="spofy-remnanode" \
      org.opencontainers.image.base.name="remnawave/node:3.4.5" \
      org.opencontainers.image.revision="${XRAY_REV}"
