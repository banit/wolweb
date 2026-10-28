# Builds from the local checkout (no git clone during the build). Several architectures without emulation:
#   docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7 .
# Go compiles on the build machine's platform for the target platform.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG TARGETOS TARGETARCH TARGETVARIANT
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/wolweb .

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/wolweb /wolweb
WORKDIR /
ENV WOLWEB_DATA_DIR=/data
VOLUME ["/data"]
EXPOSE 8089
# Runs as root inside the container so that CAP_NET_RAW (ARP scan, ping) takes effect; all other
# privileges are dropped in docker-compose.yml.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/wolweb", "-c", "/config/config.json", "-healthcheck"]
ENTRYPOINT ["/wolweb", "-c", "/config/config.json", "-d", "/data/legacy-devices.json"]
