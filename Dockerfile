# syntax=docker/dockerfile:1

FROM node:24-bookworm AS ui
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26-bookworm AS build
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

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/loom /usr/local/bin/loom
COPY --from=build --chown=nonroot:nonroot /out/data /data
WORKDIR /data
USER nonroot:nonroot
EXPOSE 5678
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/loom", "serve", "--addr", ":5678", "--data", "/data"]
