#!/bin/sh
# Startet wolweb für die Browsertests: frisch gebaut, leeres Datenverzeichnis, kurze Fristen.
# Pakete gehen an 127.0.0.1 und bleiben so auf dem Rechner.
set -e
dir=$(mktemp -d)
cd "$(dirname "$0")/.."
go build -o "$dir/wolweb" .
export TZ=UTC WOLWEB_DATA_DIR="$dir/data" WOLWEB_HOST=127.0.0.1 WOLWEB_PORT="${PORT:-8399}" WOLWEB_LANGUAGE=de \
  WOLWEB_BCASTIP=127.0.0.1 WOLWEB_WAKE_TIMEOUT=8s WOLWEB_WAKE_RETRY=2s
# Zwei erfundene Suchtreffer (192.168.1.x, lokal verwaltete MACs): einer mit Hostnamen, einer ohne.
mkdir -p "$dir/data"
now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
cat > "$dir/data/discovered.json" <<EOF
[{"mac":"02:00:00:00:10:01","ip":"192.168.1.40","hostname":"mediapc.example.com","first_seen":"$now","last_seen":"$now"},
 {"mac":"02:00:00:00:10:02","ip":"192.168.1.41","first_seen":"$now","last_seen":"$now"}]
EOF
exec "$dir/wolweb" -c "$dir/keine-config.json" -d "$dir/keine-devices.json"
