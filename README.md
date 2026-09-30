# wolweb

Wake on LAN mit Weboberfläche – Neufassung von [sameerdhoot/wolweb](https://github.com/sameerdhoot/wolweb) in Go,
als einzelne Datei ohne externe Abhängigkeiten im Browser.

- **Übersicht** mit Online-Status jedes Geräts (Ping und TCP-Ports), Suche und Filter, hell/dunkel
- **Feste Wecklinks** `/wake/<Name>`: im Browser eine Bestätigungsseite, die das Gerät prüft, das Magic Packet
  sendet und den Start verfolgt, bis das Gerät wirklich online ist
- **Netzwerksuche**: findet eingeschaltete Geräte in freigegebenen Segmenten (IP, MAC, Hostname, Hersteller) –
  per Hand oder regelmäßig im Hintergrund; neue Geräte mit einem Klick übernehmen oder ignorieren
- Übernimmt die `devices.json` und die Einstellungen des alten wolweb

## Feste Wecklinks

| Aufruf | Ergebnis |
|---|---|
| `http://host:8089/wake/NAS` im Browser | Weckseite mit Fortschritt: *Gerät prüfen → Magic Packet senden → Auf Start warten → Online* |
| `curl http://host:8089/wake/NAS` | weckt sofort, Antwort JSON `{"success":true,"message":…}` (wie im Original) |
| `curl "http://host:8089/wake/NAS?wait=120"` | weckt und wartet bis zu 120 s, bis das Gerät antwortet (`"online": true/false`) |
| `…/wake/NAS?format=html` / `?format=json` | Ausgabe erzwingen |

Der Name ist nicht von Groß-/Kleinschreibung abhängig. Die Bestätigung „online“ gibt es nur, wenn eine IP bekannt ist
(eingetragen oder aus der Netzwerksuche). Läuft das Gerät schon, sagt die Seite das und sendet nichts.

Online gilt ein Gerät, wenn es auf Ping antwortet oder einer der Ports (`status.ports`) eine Verbindung annimmt **oder
ablehnt** – eine Ablehnung kommt nur von einem laufenden Betriebssystem.

## Netzwerksuche

In `discovery.networks` (oder `WOLWEB_NETWORKS`) die Segmente eintragen, z. B. `192.168.1.0/24`. Gesucht wird nur
in Netzen, an denen der Rechner direkt hängt – ARP und Wake on LAN gehen nicht über Router. Für mehrere VLANs braucht
wolweb eine Schnittstelle in jedem VLAN (oder je VLAN eine eigene Instanz).

- **Linux mit `CAP_NET_RAW`**: aktiver ARP-Scan – findet auch Geräte, die Ping und alle Ports blocken.
- **Sonst**: jede Adresse bekommt ein UDP-Paket, danach wird die Nachbartabelle gelesen (etwas ungenauer).
- Hostnamen aus Reverse-DNS, mDNS (Apple, Linux/Avahi, Drucker …) und NetBIOS (Windows, Samba),
  Hersteller aus der eingebetteten IEEE-Liste (Wireshark `manuf`).
- Ausgeschaltete Geräte findet keine Suche – Geräte also übernehmen, solange sie laufen.

## Installation

### Docker (empfohlen)

```sh
docker compose up -d --build
```

`docker-compose.yml` nutzt `network_mode: host` (nötig für Broadcasts und ARP) und gibt dem Container nur
`NET_RAW`. Daten liegen in `./data`. Eine alte `devices.json` als `./data/legacy-devices.json` ablegen – sie wird beim
ersten Start übernommen. Docker Desktop (Windows/macOS) kann kein echtes Host-Netz, dort funktionieren Wecken und
Suche nicht.

### Direkt / Proxmox-LXC

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=1.0.0" -o wolweb .
```

Nach `/opt/wolweb` kopieren, `config.example.json` als `config.json` anpassen, `deploy/wolweb.service` nach
`/etc/systemd/system/` (Benutzer `wolweb` anlegen, `data/` beschreibbar machen), dann
`systemctl enable --now wolweb`.

## Konfiguration

`config.json` (siehe `config.example.json`) – alles hat Standardwerte, die Datei darf fehlen. Umgebungsvariablen
überschreiben die Datei.

| Schlüssel | Variable | Standard | Bedeutung |
|---|---|---|---|
| `host`, `port` | `WOLWEBHOST`, `WOLWEBPORT` | `0.0.0.0`, `8089` | Adresse der Weboberfläche |
| `vdir` | `WOLWEBVDIR` | `/` | Unterpfad, z. B. `/wolweb` hinter einem Reverse-Proxy |
| `bcastip` | `WOLWEBBCASTIP` | `192.168.1.255:9` | Standardziel, wenn weder Gerät noch Netz eins liefern |
| `read_only` | `WOLWEBREADONLY` | `false` | nur wecken, nichts ändern |
| `data_dir` | `WOLWEB_DATA_DIR` | `data` | Geräte (`devices.json`) und Suchergebnisse (`discovered.json`) |
| `discovery.networks` | `WOLWEB_NETWORKS` | – | Segmente für die Suche (CIDR, kommagetrennt) |
| `discovery.interval` | `WOLWEB_SCAN_INTERVAL` | `30m` | automatische Suche, `0` = aus |
| `discovery.max_hosts` | – | `1024` | größtes erlaubtes Segment |
| `status.interval` | `WOLWEB_STATUS_INTERVAL` | `30s` | Online-Prüfung aller Geräte |
| `status.ports` | – | 22, 80, 443, 445, … | Ports für die Online-Prüfung |
| `status.wake_timeout` | `WOLWEB_WAKE_TIMEOUT` | `3m` | so lange wartet die Weckseite |
| `auth.username/password` | `WOLWEB_USER`, `WOLWEB_PASSWORD` | – | Anmeldung (HTTP Basic) |
| `auth.api_token` | `WOLWEB_TOKEN` | – | `Authorization: Bearer …` für Automationen |
| `auth.public_wake` | `WOLWEB_PUBLIC_WAKE` | `true` | Wecklinks funktionieren ohne Anmeldung |

Die Broadcast-Adresse eines Geräts wird automatisch aus dem Netz seiner IP bestimmt; nur bei Sonderfällen im
Gerät unter „Erweitert“ eintragen.

## API

Alle ändernden Aufrufe brauchen den Kopf `X-WolWeb: 1` (Schutz gegen Cross-Site-Requests) oder ein Bearer-Token.

| Methode | Pfad | |
|---|---|---|
| GET | `/api/v1/info` | Einstellungen, lokale Netze |
| GET / POST | `/api/v1/devices` | Liste (mit Status) / anlegen |
| PUT / DELETE | `/api/v1/devices/{id}` | ändern (`updated_at` mitschicken schützt vor Überschreiben) / löschen |
| POST | `/api/v1/devices/{id}/wake` | Magic Packet senden |
| GET | `/api/v1/devices/{id}/status` | sofort prüfen |
| GET | `/api/v1/discovery` | letzte Suche und Treffer |
| POST | `/api/v1/discovery/scan`, `/cancel` | Suche starten / abbrechen |
| POST | `/api/v1/discovery/{mac}/ignore` | `{"ignored":true}` |
| GET | `/wake/{name}`, `/health`, `/data/get` | wie im Original |

## Entwicklung

```sh
go test ./...
go run . -c config.json
```

Die Oberfläche liegt in `internal/web/` (Vorlagen und statische Dateien, eingebettet per `go:embed`).
Herstellerliste aktualisieren: `sh tools/gen-oui.sh`.
