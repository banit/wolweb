#!/bin/sh
# Erzeugt internal/oui/oui.txt.gz aus der Wireshark-Herstellerliste.
# Zeilenformat: <Präfix in Hex (6, 7 oder 9 Stellen)>\t<Hersteller>
# Erst herunterladen, umwandeln und prüfen, dann ersetzen: Ein Fehler (Netz, Server, leere oder
# abgeschnittene Antwort) lässt die vorhandene Liste unverändert und endet mit Exit-Code 1.
set -eu
out="$(dirname "$0")/../internal/oui/oui.txt.gz"
min=40000 # die Liste hat gut 58 000 Einträge; deutlich weniger heißt: Download kaputt
tmp=$(mktemp -d)
trap 'rm -rf "$tmp" "$out.new"' EXIT
curl -fsSL -o "$tmp/manuf" https://www.wireshark.org/download/automated/data/manuf
grep -v '^#' "$tmp/manuf" \
  | awk -F'\t' 'NF>=2 { p=$1; gsub(/[ ]+$/,"",p); bits=24; if (index(p,"/")) { split(p,a,"/"); p=a[1]; bits=a[2] }
      gsub(/[:.-]/,"",p); if (length(p)<6) next; name=(NF>=3 && $3!="")?$3:$2; gsub(/[ ]+$/,"",name);
      print toupper(substr(p,1,bits/4)) "\t" name }' > "$tmp/oui.txt"
n=$(wc -l < "$tmp/oui.txt")
if [ "$n" -lt "$min" ]; then
  echo "gen-oui: only $n entries (expected at least $min) – $out left unchanged" >&2
  exit 1
fi
gzip -9 < "$tmp/oui.txt" > "$out.new"
mv "$out.new" "$out"
echo "gen-oui: $n entries written to $out"
