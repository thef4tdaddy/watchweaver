# syntax=docker/dockerfile:1.7
FROM node:22-alpine AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run test && npm run build

FROM golang:1.22-alpine AS backend
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN rm -rf internal/server/static
COPY --from=frontend /src/web/dist ./internal/server/static
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/watchweaver ./cmd/watchweaver

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && addgroup -S -g 10001 watchweaver && adduser -S -D -H -u 10001 -G watchweaver watchweaver && mkdir -p /data/backups /data/exports && chown -R watchweaver:watchweaver /data
COPY --from=backend /out/watchweaver /usr/local/bin/watchweaver
USER watchweaver
VOLUME ["/data"]
EXPOSE 8080
ENV WATCHWEAVER_LISTEN_ADDR=:8080 WATCHWEAVER_DATABASE=/data/watchweaver.db
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD wget -qO- http://127.0.0.1:8080/readyz >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/watchweaver"]
