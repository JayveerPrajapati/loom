# syntax=docker/dockerfile:1

# Base images are digest-pinned for reproducible builds; the tag stays in the
# reference as the intended line, and Dependabot proposes digest bumps. The
# digest must be the multi-arch index, not a per-architecture manifest — an
# amd64-only pin silently emulates on arm64 hosts (and breaks native tooling).
FROM node:24-bookworm@sha256:9d266751ca3d7fdbe651c878ffd5ad0a42fed49a18c283509267c97f025b7496 AS ui
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26-bookworm@sha256:dc40a13b61304647ed7e5d581d246b86413dfc29d63c6d018310feb93cd98fc0 AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/loom ./cmd/loom && \
    mkdir -p /out/data && touch /out/data/.keep

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/loom /usr/local/bin/loom
COPY --from=build --chown=nonroot:nonroot /out/data /data
WORKDIR /data
USER nonroot:nonroot
EXPOSE 5678
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/loom", "serve", "--addr", ":5678", "--data", "/data"]
