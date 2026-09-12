# ── Stage 1: Build frontend ───────────────────────────────────────────────────
FROM node:24-alpine AS frontend

WORKDIR /frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --prefer-offline

COPY frontend .
RUN npm run build

# ── Stage 2: Build Go binary ──────────────────────────────────────────────────
FROM golang:1.26-alpine AS gobuilder

WORKDIR /build

COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend .

# Overlay the compiled frontend into the embed path.
COPY --from=frontend /frontend/dist ./internal/ui/dist

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /app ./cmd/server

# ── Stage 3: Runtime ──────────────────────────────────────────────────────────
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
