# BUILDPLATFORM is one of BuildKit's automatic build-time args, populated by
# `docker buildx build` (which is how CI's multi-platform image is built — see
# .github/workflows/ci.yml) so the two stages below run natively rather than
# under emulation. It is NOT populated by the classic builder `docker compose
# build` falls back to when the buildx CLI plugin is missing — see #297 — and
# an unset $BUILDPLATFORM makes `--platform=$BUILDPLATFORM` expand to
# `--platform=`, which fails to parse rather than being treated as "no
# constraint". The default here only ever applies on that fallback path: a
# real buildx invocation always supplies its own value, which wins over this
# one. It costs an ordinary single-arch build on non-amd64 hardware without
# buildx one emulated pair of build stages (a few extra minutes, not a
# failure) — the one case this default cannot make native, because nothing
# here can see the host's actual architecture. Bare `docker build`, and any
# build under an installed buildx plugin, are unaffected either way.
ARG BUILDPLATFORM=linux/amd64

# ── Stage 1: Build frontend ───────────────────────────────────────────────────
#
# Pinned to BUILDPLATFORM: the output is static JS/CSS with no architecture of
# its own, so running node under emulation to produce identical bytes is pure
# cost. On an arm64 host building for amd64 this is the difference between a
# native npm build and an emulated one.
FROM --platform=$BUILDPLATFORM node:24-alpine AS frontend

WORKDIR /frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --prefer-offline

COPY frontend .
RUN npm run build

# ── Stage 2: Build Go binary ──────────────────────────────────────────────────
#
# Also pinned to BUILDPLATFORM, and cross-compiles instead. Go is a cross
# compiler by default and the binary is CGO_ENABLED=0, so targeting another
# architecture costs nothing — whereas emulating the whole toolchain under QEMU
# turns a two-minute compile into fifteen or more.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS gobuilder

WORKDIR /build

COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend .

# Overlay the compiled frontend into the embed path.
COPY --from=frontend /frontend/dist ./internal/ui/dist

# Supplied by the builder, not by us: for `--platform linux/arm64` these are
# linux, arm64 and "". TARGETVARIANT carries the ARM revision ("v7", "v6"),
# which Go wants as a bare number in GOARM, hence the ${...#v} strip. An empty
# GOARM/GOARCH is ignored by the toolchain, so a plain `docker build` with no
# --platform still produces a host-native binary exactly as before.
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

RUN CGO_ENABLED=0 \
    GOOS=${TARGETOS:-linux} \
    GOARCH=${TARGETARCH} \
    GOARM=${TARGETVARIANT#v} \
    go build -trimpath -o /app ./cmd/server

# Fail the build here rather than at `docker run` on someone else's machine.
# Getting this wrong produces an image that looks correct, passes a build, and
# dies with "exec format error" only on the architecture we do not test on.
RUN set -eu; \
    got="$(go version -m /app | sed -n 's/.*GOARCH=//p' | head -1)"; \
    want="${TARGETARCH:-$(go env GOARCH)}"; \
    if [ "$got" != "$want" ]; then \
        echo "ERROR: built for GOARCH=$got but this image targets $want" >&2; \
        exit 1; \
    fi; \
    echo "binary GOARCH=$got matches target"

# ── Stage 3: Runtime ──────────────────────────────────────────────────────────
#
# Deliberately NOT pinned to BUILDPLATFORM — this stage must be the target
# architecture, since it is what actually ships. Its RUN steps (apk, adduser)
# are emulated when cross-building, which is fine: they are seconds of work,
# unlike the compilers above.
FROM alpine:3.23.5

# ca-certificates only, and it is required: the server makes outbound HTTPS
# calls to the identity provider for OIDC discovery and JWKS, and Alpine ships
# no root store for Go's TLS stack to verify against.
#
# Nothing else belongs here. The binary is CGO_ENABLED=0 and never shells out,
# so any other package is reachable by an attacker who gets execution in this
# container without being reachable by the application.
RUN apk add --no-cache ca-certificates

# uid and gid are pinned, not left to allocation order.
#
# The user was named ohd before the rename to Go Help Desk, and got uid 100 /
# gid 101 as the first free system ids. docker-compose bind-mounts
# ./data/attachments to /data, so those numbers are baked into file ownership
# on every existing deployment — a rename that shifted them would leave the
# attachments directory unwritable, and only once someone uploaded a file.
#
# Pinning also means a future edit that adds another user above this one cannot
# silently move them.
RUN addgroup -S -g 101 ghd && adduser -S -u 100 -G ghd ghd

WORKDIR /app
COPY --from=gobuilder /app /app/server

RUN mkdir -p /data/attachments && chown -R ghd:ghd /data

USER ghd

EXPOSE 8080

ENTRYPOINT ["/app/server"]
