# Baut aus dem lokalen Stand (kein git clone im Build).
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/wolweb .

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/wolweb /wolweb
WORKDIR /
ENV WOLWEB_DATA_DIR=/data
VOLUME ["/data"]
EXPOSE 8089
# Läuft als root im Container, damit CAP_NET_RAW (ARP-Scan, Ping) wirkt; alle anderen Rechte
# werden in docker-compose.yml entzogen.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/wolweb", "-c", "/config/config.json", "-healthcheck"]
ENTRYPOINT ["/wolweb", "-c", "/config/config.json", "-d", "/data/legacy-devices.json"]
