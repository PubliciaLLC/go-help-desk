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

RUN apk add --no-cache \
    ca-certificates \
    openssl \
    curl

# For Alpine-based images
RUN apk update && apk upgrade --no-cache

# Install EDTN CA chain into the runtime trust store
COPY certs/* /usr/local/share/ca-certificates/

RUN update-ca-certificates

RUN addgroup -S ohd && adduser -S ohd -G ohd

WORKDIR /app
COPY --from=gobuilder /app /app/server

RUN mkdir -p /data/attachments && chown -R ohd:ohd /data

USER ohd

EXPOSE 8080

ENTRYPOINT ["/app/server"]
