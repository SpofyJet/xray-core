# Spofy remnanode image: stock remnawave/node with the Spofy Xray build swapped in.
# Only /usr/local/bin/xray changes (rw-core is a symlink to it); rw-node itself is untouched.
ARG BASE=remnawave/node:3.4.4@sha256:19ca38195f699f59c0e5978489a18173234aca4b1bd29bab495b16e9d6423f70
FROM ${BASE}
ARG XRAY_REV
COPY xray /usr/local/bin/xray
RUN chmod 0755 /usr/local/bin/xray && /usr/local/bin/xray version | head -1 | grep -q "spofy-" \
    && ls -l /usr/local/bin/rw-core
LABEL org.opencontainers.image.title="spofy-remnanode" \
      org.opencontainers.image.base.name="remnawave/node:3.4.4" \
      org.opencontainers.image.revision="${XRAY_REV}"
