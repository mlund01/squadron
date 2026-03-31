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
ENV SQUADRON_HOME=/data/squadron
WORKDIR /config
VOLUME ["/config", "/data/squadron"]
ENTRYPOINT ["squadron"]
