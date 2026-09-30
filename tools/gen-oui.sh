#!/bin/sh
# Erzeugt internal/oui/oui.txt.gz aus der Wireshark-Herstellerliste.
# Zeilenformat: <Präfix in Hex (6, 7 oder 9 Stellen)>\t<Hersteller>
set -e
curl -sL https://www.wireshark.org/download/automated/data/manuf \
  | grep -v '^#' \
  | awk -F'\t' 'NF>=2 { p=$1; gsub(/[ ]+$/,"",p); bits=24; if (index(p,"/")) { split(p,a,"/"); p=a[1]; bits=a[2] }
      gsub(/[:.-]/,"",p); if (length(p)<6) next; name=(NF>=3 && $3!="")?$3:$2; gsub(/[ ]+$/,"",name);
      print toupper(substr(p,1,bits/4)) "\t" name }' \
  | gzip -9 > "$(dirname "$0")/../internal/oui/oui.txt.gz"
