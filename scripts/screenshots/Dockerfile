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
# A tag, not a digest: ubuntu:24.04 moves with security rebuilds, and so do the
# apt packages below (postgresql-16, fontconfig). Node and Go are pinned and
# checked against the checksums their own sites publish.
ARG NODE_VERSION=v24.22.3
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
    cd /tmp; \
    node_tar="node-${NODE_VERSION}-linux-${node_arch}.tar.xz"; \
    curl -fsSLO "https://nodejs.org/dist/${NODE_VERSION}/${node_tar}"; \
    curl -fsSL "https://nodejs.org/dist/${NODE_VERSION}/SHASUMS256.txt" | grep " ${node_tar}\$" | sha256sum -c -; \
    tar -xJf "$node_tar" -C /usr/local --strip-components=1; \
    go_tar="go${GO_VERSION}.linux-${TARGETARCH}.tar.gz"; \
    curl -fsSLO "https://go.dev/dl/${go_tar}"; \
    go_sum="$(curl -fsSL 'https://go.dev/dl/?mode=json&include=all' | GO_TAR="$go_tar" python3 -c "import json,os,sys; print(next(f['sha256'] for r in json.load(sys.stdin) for f in r['files'] if f['filename']==os.environ['GO_TAR']))")"; \
    echo "${go_sum}  ${go_tar}" | sha256sum -c -; \
    tar -xzf "$go_tar" -C /usr/local; \
    rm -f "$node_tar" "$go_tar"; \
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
