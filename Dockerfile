# syntax=docker/dockerfile:1.7
# Multi-stage, multi-arch image for minimax-music.
#
# Produces a single static binary on a distroless base for every supported
# platform (linux/amd64, linux/arm64) with no per-arch C toolchain. Each
# platform slice runs its matching golang:1.24 image (the arm64 slice under
# QEMU), and buildx injects TARGETOS/TARGETARCH so Go builds for that slice's
# arch. The artifact is fully static, so it runs on the distroless runtime base
# unchanged on any arch.
#
# TARGETOS/TARGETARCH are declared WITHOUT a default on purpose. A default
# (e.g. `ARG TARGETARCH=amd64`) shadows buildx's per-platform injection: every
# slice then builds with GOARCH=amd64, so the arm64 manifest ships an amd64
# binary and fails on arm64 hosts with "exec format error".

# --- build stage -------------------------------------------------------------
FROM golang:1.24 AS build
WORKDIR /src

# Cache deps first: copy only the module manifests so `go mod download` is a
# layer that rarely invalidates and survives image rebuilds / CI runs.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Source + build. CGO is off and debug info is stripped, so the artifact is
# fully static and runs on a no-libc base on any arch. The cache mounts on
# /go/pkg/mod and the go-build cache keep incremental rebuilds fast.
#
# TARGETOS/TARGETARCH get NO default here: a default would shadow buildx's
# per-platform value (see header) and break the arm64 slice.
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/minimax-music .

# --- runtime stage -----------------------------------------------------------
# distroless base-debian12: non-root, no shell, no package manager — but it
# DOES ship the CA certificate bundle and tzdata, which the service needs
# (every outbound call to www.minimaxi.com / cdn.hailuoai.com / the WSS endpoint
# is TLS, and Go's SystemCertPool reads the Debian bundle from here). A static
# binary needs no glibc, so it runs on this base unchanged.
FROM gcr.io/distroless/base-debian12:nonroot AS runtime

WORKDIR /app
COPY --from=build /out/minimax-music /app/minimax-music

# Default listen addr is ":8080" (all interfaces), overridable via MINIMAX_ADDR.
# Non-root (uid 65532) can bind ports >= 1024, so 8080 is fine.
EXPOSE 8080

# distroless has no shell: exec the binary directly (not via a shell form).
ENTRYPOINT ["/app/minimax-music"]
