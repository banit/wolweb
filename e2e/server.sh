#!/bin/sh
# Startet wolweb für die Browsertests: frisch gebaut, leeres Datenverzeichnis, kurze Fristen.
# Vom echten Netz bleibt der Testserver getrennt: Weckrufe gehen an 127.0.0.1, die automatische
# Netzwerksuche ist aus, und die erfundenen Suchtreffer liegen auf 127.0.0.x. Nur das „offline“-
# Testgerät (helpers.js) wird im Dokumentationsnetz 198.51.100.0/24 geprüft – dort gibt es kein
# echtes Gerät, und eine Adresse auf diesem Rechner antwortete immer.
# Beim Beenden (SIGTERM von Playwright, siehe playwright.config.js, oder screenshots.js) wird
# wolweb gestoppt und das temporäre Verzeichnis samt Programm gelöscht.
set -e
dir=$(mktemp -d)
pid=
cleanup() {
  if [ -n "$pid" ]; then
    kill "$pid" 2>/dev/null || :
    wait "$pid" 2>/dev/null || :
  fi
  rm -rf "$dir"
}
trap cleanup EXIT
trap 'exit 143' TERM
trap 'exit 130' INT
cd "$(dirname "$0")/.."
go build -o "$dir/wolweb" .
export TZ=UTC WOLWEB_DATA_DIR="$dir/data" WOLWEB_HOST=127.0.0.1 WOLWEB_PORT="${PORT:-8399}" WOLWEB_LANGUAGE=de \
  WOLWEB_BCASTIP=127.0.0.1 WOLWEB_WAKE_TIMEOUT="${WOLWEB_WAKE_TIMEOUT:-8s}" WOLWEB_WAKE_RETRY="${WOLWEB_WAKE_RETRY:-2s}" \
  WOLWEB_SCAN_INTERVAL=0
# Zwei erfundene Suchtreffer (lokal verwaltete MACs): einer mit Hostnamen, einer ohne.
mkdir -p "$dir/data"
now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
cat > "$dir/data/discovered.json" <<EOF
[{"mac":"02:00:00:00:10:01","ip":"127.0.0.40","hostname":"mediapc.example.com","first_seen":"$now","last_seen":"$now"},
 {"mac":"02:00:00:00:10:02","ip":"127.0.0.41","first_seen":"$now","last_seen":"$now"}]
EOF
"$dir/wolweb" -c "$dir/keine-config.json" -d "$dir/keine-devices.json" &
pid=$!
wait "$pid"
