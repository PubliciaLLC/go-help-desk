# Build environment for scripts/screenshots.sh on a machine that is not Linux
# (a Mac with Docker Desktop, say). Use scripts/screenshots/docker.sh, which
# builds this image, mounts the checkout and runs the script inside it.
#
# One container holds everything the script wants on PATH: Go (the version in
# backend/go.mod), Node, a PostgreSQL 16 server (started by entrypoint.sh, role
# helpdesk/helpdesk, which is the script's default admin URL), fontconfig, git,
# and the Chromium revision the script and capture.mjs pin.
FROM ubuntu:24.04

ARG GO_VERSION
ARG NODE_MAJOR=24
# Playwright 1.56 is the release whose Chromium is revision 1194, the build
# that capture.mjs and screenshots.sh pin (/opt/pw-browsers/chromium-1194).
# The frontend's own Playwright (1.63) drives it through executablePath.
ARG PLAYWRIGHT_CLI=1.56.1
ARG TARGETARCH

ENV DEBIAN_FRONTEND=noninteractive \
    PLAYWRIGHT_BROWSERS_PATH=/opt/pw-browsers \
    PLAYWRIGHT_SKIP_VALIDATE_HOST_REQUIREMENTS=1 \
    PATH=/usr/local/go/bin:/usr/lib/postgresql/16/bin:$PATH \
    GOFLAGS=-buildvcs=false \
    TERM=dumb

RUN test -n "$GO_VERSION" || { echo "build with --build-arg GO_VERSION=<version from backend/go.mod>" >&2; exit 1; }

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates curl xz-utils git python3 procps fontconfig \
      postgresql-16 postgresql-client-16 \
 && rm -rf /var/lib/apt/lists/*

RUN set -eux; \
    case "$TARGETARCH" in amd64) node_arch=x64 ;; arm64) node_arch=arm64 ;; *) exit 1 ;; esac; \
    ver="$(curl -fsSL https://nodejs.org/dist/index.json | python3 -c "import json,sys; print(next(x['version'] for x in json.load(sys.stdin) if x['version'].startswith('v${NODE_MAJOR}.')))")"; \
    curl -fsSL "https://nodejs.org/dist/$ver/node-$ver-linux-$node_arch.tar.xz" | tar -xJ -C /usr/local --strip-components=1; \
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz" | tar -xz -C /usr/local; \
    node --version; go version

# Chromium and the system libraries and fonts it needs. The CLI is fetched
# once into a throwaway npx cache; nothing from it stays in the image.
RUN npx --yes playwright@${PLAYWRIGHT_CLI} install --with-deps chromium \
 && rm -rf /var/lib/apt/lists/* /root/.npm \
 && test -x /opt/pw-browsers/chromium-1194/chrome-linux/chrome

RUN git config --system --add safe.directory '*'

COPY entrypoint.sh /usr/local/bin/ghd-shots-entrypoint
RUN chmod 755 /usr/local/bin/ghd-shots-entrypoint
ENTRYPOINT ["/usr/local/bin/ghd-shots-entrypoint"]
