# wolweb – Arbeitsnotizen

Neufassung von sameerdhoot/wolweb (Go 1.27, Modul `wolweb`, keine Web-Frameworks, Oberfläche ohne CDN/Build-Schritt).

## Aufbau
- `main.go` Start, Hintergrund-Suche, `-healthcheck`
- `internal/config` Konfiguration + Umgebungsvariablen (alte Namen WOLWEBHOST … bleiben gültig)
- `internal/store` Geräte/Suchtreffer, atomares Speichern (Temp-Datei + Rename), Import des alten Formats
  (dort ist `ip` die Broadcast-Adresse!)
- `internal/wol` Magic Packet, `internal/probe` Ping + TCP (abgelehnt = online), `internal/status` Monitor
- `internal/discovery` ARP-Scan (Linux, mdlayher/arp, CAP_NET_RAW) mit Rückfall Nachbartabelle; Namen per DNS/mDNS/NetBIOS
- `internal/oui` eingebettete Herstellerliste (`tools/gen-oui.sh`)
- `internal/web` Handler, Vorlagen (`templates/`), statische Dateien (`static/`: app.js, wake.js, app.css)

## Arbeitsweise
- Deutsch in Texten, Kommentaren und Doku. Nach Änderungen: `go vet ./...`, `GOOS=linux go vet ./...`, `go test ./...`.
- Go liegt unter `C:\dev\_tools\go\bin` (nicht im PATH).
- CSP erlaubt keine Inline-Skripte/-Styles: Daten per `data-*`, Styles per CSSOM (`el.style.x = …`) setzen.
- Ändernde Anfragen brauchen `X-WolWeb: 1` (CSRF), Bearer-Token ist ausgenommen.
- Texte aus dem Netz (Hostnamen) nur per `textContent` ausgeben.

## Stand (30.09.2026, 1.0.1)
- 1.0.1: Gegenprüfung mit Codex eingearbeitet (Wecklinks von fremden Seiten nur als Weckseite via Sec-Fetch-Site,
  IP-Identität per Nachbartabelle, Suchtreffer-IPs verfallen nach 7 Tagen, Status an Ziel-IP gebunden, ARP mit
  richtiger Absender-IP und Abbruch, fsync des Verzeichnisses, Ladefehler brechen den Start ab statt Daten zu überschreiben).
- Lokal unter Windows getestet: Übersicht, Anlegen aus Suche, Weckseite (läuft bereits / senden → online),
  Suche per Nachbartabelle. Nicht getestet: ARP-Scan unter Linux, Docker-Image, Zeitüberschreitung der Weckseite,
  Handy-Ansicht im Browser.
