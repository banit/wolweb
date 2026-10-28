#!/bin/sh
# Startet wolweb für die Browsertests: frisch gebaut, leeres Datenverzeichnis, kurze Fristen.
# Pakete gehen an 127.0.0.1 und bleiben so auf dem Rechner.
set -e
dir=$(mktemp -d)
cd "$(dirname "$0")/.."
go build -o "$dir/wolweb" .
export WOLWEB_DATA_DIR="$dir/data" WOLWEB_HOST=127.0.0.1 WOLWEB_PORT="${PORT:-8399}" WOLWEB_LANGUAGE=de \
  WOLWEB_BCASTIP=127.0.0.1 WOLWEB_WAKE_TIMEOUT=8s WOLWEB_WAKE_RETRY=2s
exec "$dir/wolweb" -c "$dir/keine-config.json" -d "$dir/keine-devices.json"
