# One image holds all four binaries (api, worker, mockprovider, seed);
# docker-compose.yml picks which one each service runs.

FROM golang:1.27-alpine AS build
WORKDIR /src
# Dependencies first: this layer stays cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Static binaries (no cgo); migrations are embedded with go:embed.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/...

FROM alpine:3.22
# busybox wget is used by the compose healthchecks.
RUN adduser -D -H -u 10001 app
COPY --from=build /out/ /usr/local/bin/
USER app
