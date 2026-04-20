# syntax=docker/dockerfile:1.7
#
# Stage 1: Build. Runs natively on the builder's platform (e.g. x86_64)
# and cross-compiles to the target architecture using Buildx's
# $BUILDPLATFORM / $TARGETOS / $TARGETARCH build args.
FROM --platform=$BUILDPLATFORM golang:1.25.5-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /src

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Pure-Go sqlite (modernc.org/sqlite) so we can cross-compile without CGO.
ENV CGO_ENABLED=0 GOSUMDB=off
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
        -o /out/trader ./cmd/trader

# Stage 2: Minimal runtime on the target arch.
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 trader \
    && mkdir -p /app/data \
    && chown -R trader:trader /app

USER trader
WORKDIR /app

COPY --from=builder /out/trader /app/trader

# Default DATABASE_URL lives on the writable /app/data volume. Override
# via env when you bring your own persistence.
ENV DATABASE_URL=file:/app/data/trader.db?_time_format=sqlite&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on) \
    HTTP_ADDR=:8080 \
    APP_ENV=production

EXPOSE 8080
VOLUME ["/app/data"]

ENTRYPOINT ["/app/trader"]
