FROM golang:1.25-alpine AS builder
ARG VERSION=dev
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X squadron/cmd.Version=${VERSION}" -o squadron ./cmd/cli

FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /build/squadron /usr/local/bin/squadron
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
ENV SQUADRON_CONTAINER=1
WORKDIR /config
# No VOLUME directive: we want the entrypoint to fail fast if /config
# isn't mounted. Declaring VOLUME would cause Docker to create anonymous
# volumes automatically, defeating the mount check.
# State lives in /config/.squadron/ alongside the HCL config files.
ENTRYPOINT ["docker-entrypoint.sh"]
