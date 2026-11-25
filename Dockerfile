# syntax=docker/dockerfile:1
#
# Production image of the API: one static binary on a distroless base (no shell, no package manager, non-root).
# The goose migrations and the provinces/wards snapshot are embedded in the binary, so the same image also runs
# `migrate` and `seed`:
#   docker run --rm -e DATABASE_URL=... <image> migrate up
#   docker run --rm -e DATABASE_URL=... <image> seed
#
# There is no HEALTHCHECK: a distroless static image has no shell or curl to run one. Point the orchestrator's probe at
# GET /healthz (liveness) and GET /readyz (readiness: pings Postgres and Redis) on the port in HTTP_ADDR (8080).
#
# This file is only written, never built by tooling here: build it yourself, e.g.
#   docker build -t rental-management-api-go:local .

FROM golang:1.27.4 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-mod=readonly

# Dependencies first, so editing code does not download them again.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/rental-management ./cmd/rental-management

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/rental-management /rental-management
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/rental-management"]
CMD ["api"]
