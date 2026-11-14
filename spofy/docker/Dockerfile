# Spofy remnanode image: stock remnawave/node with the Spofy Xray build swapped in.
# Only /usr/local/bin/xray changes (rw-core is a symlink to it); rw-node itself is untouched.
ARG BASE=remnawave/node:3.4.6@sha256:3d5fde656a36920f53c5c080f2dc821df8318f453803b07c4e5242752a549d69
FROM ${BASE}
ARG XRAY_REV
COPY xray /usr/local/bin/xray
RUN chmod 0755 /usr/local/bin/xray && /usr/local/bin/xray version | head -1 | grep -q "spofy-" \
    && ls -l /usr/local/bin/rw-core
LABEL org.opencontainers.image.title="spofy-remnanode" \
      org.opencontainers.image.base.name="remnawave/node:3.4.6" \
      org.opencontainers.image.revision="${XRAY_REV}"
