# syntax=docker/dockerfile:1
FROM docker.io/library/golang:1.22-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/tiny-shortener .

FROM docker.io/library/alpine:3.20
COPY --from=build /out/tiny-shortener /usr/local/bin/tiny-shortener
# Runs as non-root uid 1001 (numeric — no /etc/passwd entry needed for a static
# Go binary). /data is provided by the PVC mount at runtime; k8s fsGroup owns it.
# No adduser/chown here: the runner's buildah is rootless (single uid mapping),
# so creating users or chowning to other uids fails. OPS-1210.
USER 1001
EXPOSE 8080
ENTRYPOINT ["tiny-shortener"]
