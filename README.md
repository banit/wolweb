# wolweb

Wake on LAN mit Weboberfläche – Neufassung von [sameerdhoot/wolweb](https://github.com/sameerdhoot/wolweb) in Go,
als einzelne Datei ohne externe Abhängigkeiten im Browser.

- **Übersicht** mit Online-Status jedes Geräts (Ping und TCP-Ports), Suche und Filter, hell/dunkel
- **Feste Wecklinks** `/wake/<Name>`: im Browser eine Bestätigungsseite, die das Gerät prüft, das Magic Packet
  sendet und den Start verfolgt, bis das Gerät wirklich online ist
- **Netzwerksuche**: findet eingeschaltete Geräte in freigegebenen Segmenten (IP, MAC, Hostname, Hersteller) –
  per Hand oder regelmäßig im Hintergrund; neue Geräte mit einem Klick übernehmen oder ignorieren
- **Verlauf** jedes Weckversuchs (wer, wann, woher, Ergebnis), **Export/Import** der Geräte als JSON
- Übernimmt die `devices.json` und die Einstellungen des alten wolweb

## Geräte

Pflicht sind nur Name und MAC-Adresse. Die IP-Adresse braucht es für den Online-Status (sonst nimmt wolweb die IP
aus der Netzwerksuche). Unter „Erweitert“:

| Feld | Wofür |
|---|---|
| Broadcast-Adresse | Ziel des Magic Packets, nur für Sonderfälle (sonst automatisch) |
| Netzwerkschnittstelle | Paket über genau diese Schnittstelle senden (Rechner mit mehreren Netzen) |
| Ports | eigene Ports für die Online-Prüfung statt der allgemeinen Liste |
| SecureOn-Passwort | 6 Byte, falls die Netzwerkkarte ein Passwort im Magic Packet verlangt |
| Notiz | freier Text auf der Karte |

## Feste Wecklinks

| Aufruf | Ergebnis |
|---|---|
| `http://host:8089/wake/NAS` im Browser | Weckseite mit Fortschritt: *Gerät prüfen → Magic Packet senden → Auf Start warten → Online* |
| `curl http://host:8089/wake/NAS` | weckt sofort, Antwort JSON `{"success":true,"message":…}` (wie im Original) |
| `curl "http://host:8089/wake/NAS?wait=120"` | weckt und wartet bis zu 120 s, bis das Gerät antwortet (`"online": true/false`; fehlt, wenn keine IP bekannt ist) |
| `…/wake/NAS?format=html` / `?format=json` | Ausgabe erzwingen |

`HEAD`-Anfragen (Linkprüfer, Vorschauen in Messengern) wecken nie. Der Name ist nicht von Groß-/Kleinschreibung abhängig. Die Bestätigung „online“ gibt es nur, wenn eine IP bekannt ist
(eingetragen oder aus der Netzwerksuche). Läuft das Gerät schon, sagt die Seite das und sendet nichts.

Online gilt ein Gerät, wenn es auf Ping antwortet oder einer der Ports (`status.ports`) eine Verbindung annimmt **oder
ablehnt** – eine Ablehnung kommt nur von einem laufenden Betriebssystem. Das gilt für Geräte im eigenen Netzsegment;
hinter einem Router zählt nur eine angenommene Verbindung oder Ping, weil dort auch eine Firewall stellvertretend
ablehnen kann.

Geprüft wird zuerst per Ping und mit der Methode, die beim letzten Mal geklappt hat; die übrigen Ports kommen erst
0,3 s später dran. Ein Gerät, das auf Ping antwortet, bekommt so keine einzige TCP-Verbindung (kein Rauschen im
SSH-Log). Pro Gerät lassen sich unter „Erweitert“ eigene Ports eintragen, z. B. `32400` für einen Plex-Server.

## Netzwerksuche

In `discovery.networks` (oder `WOLWEB_NETWORKS`) die Segmente eintragen, z. B. `192.168.1.0/24`. Gesucht wird nur
in Netzen, an denen der Rechner direkt hängt – ARP und Wake on LAN gehen nicht über Router. Für mehrere VLANs braucht
wolweb eine Schnittstelle in jedem VLAN (oder je VLAN eine eigene Instanz).

- **Linux mit `CAP_NET_RAW`**: aktiver ARP-Scan – findet auch Geräte, die Ping und alle Ports blocken.
- **Sonst**: jede Adresse bekommt ein UDP-Paket, danach wird die Nachbartabelle gelesen (etwas ungenauer).
- Hostnamen aus Reverse-DNS, mDNS (Apple, Linux/Avahi, Drucker …) und NetBIOS (Windows, Samba),
  Hersteller aus der eingebetteten IEEE-Liste (Wireshark `manuf`).
- Ausgeschaltete Geräte findet keine Suche – Geräte also übernehmen, solange sie laufen.
- Treffer, die 30 Tage lang nicht mehr gesehen wurden, verschwinden aus der Liste (ignorierte bleiben).

## Installation

### Docker (empfohlen)

```sh
docker compose up -d --build
```

`docker-compose.yml` nutzt `network_mode: host` (nötig für Broadcasts und ARP) und gibt dem Container nur
`NET_RAW`. Daten liegen in `./data`. Docker Desktop (Windows/macOS) kann kein echtes Host-Netz, dort funktionieren
Wecken und Suche nicht.

wolweb läuft im Container als root, aber ohne jede Sonderberechtigung – es darf daher nur in Ordner schreiben, die
root gehören. Am einfachsten legt Docker `./data` beim ersten Start selbst an. Wer den Ordner vorher anlegt, etwa um
die alte `devices.json` zu übernehmen, gibt ihn root:

```sh
mkdir -p data && cp /pfad/zum/alten/devices.json data/legacy-devices.json
sudo chown -R root:root data
```

Die `legacy-devices.json` wird beim ersten Start übernommen.

### Direkt / Proxmox-LXC

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=1.0.2" -o wolweb .
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
| `bcastip` | `WOLWEBBCASTIP` | automatisch | Standardziel, wenn weder Gerät noch Netz eins liefern |
| `read_only` | `WOLWEBREADONLY` | `false` | nur wecken, nichts ändern |
| `data_dir` | `WOLWEB_DATA_DIR` | `data` | Geräte (`devices.json`), Laufzeitdaten (`state.json`) und Suchergebnisse (`discovered.json`) |
| `discovery.networks` | `WOLWEB_NETWORKS` | – | Segmente für die Suche (CIDR, kommagetrennt) |
| `discovery.interval` | `WOLWEB_SCAN_INTERVAL` | `30m` | automatische Suche, `0` = aus |
| `discovery.max_hosts` | – | `1024` | größtes erlaubtes Segment |
| `status.interval` | `WOLWEB_STATUS_INTERVAL` | `30s` | Online-Prüfung aller Geräte |
| `status.ports` | – | 22, 80, 443, 445, … | Ports für die Online-Prüfung |
| `status.wake_timeout` | `WOLWEB_WAKE_TIMEOUT` | `3m` | so lange wartet die Weckseite |
| `auth.username/password` | `WOLWEB_USER`, `WOLWEB_PASSWORD` | – | Anmeldung (HTTP Basic) |
| `auth.api_token` | `WOLWEB_TOKEN` | – | `Authorization: Bearer …` für Automationen |
| `auth.public_wake` | `WOLWEB_PUBLIC_WAKE` | `true` | Wecklinks funktionieren ohne Anmeldung |
| `auth.wake_keys` | `WOLWEB_WAKE_KEYS` | `false` | Wecklinks ohne Anmeldung nur mit dem Schlüssel des Geräts (`?key=…`) |
| `allowed_hosts` | `WOLWEB_ALLOWED_HOSTS` | – | weitere Hostnamen, unter denen wolweb erreichbar ist (siehe Sicherheit) |

`devices.json` enthält nur, was man selbst einträgt; was wolweb beobachtet (zuletzt geweckt, zuletzt online, die
letzten 500 Weckversuche) steht in `state.json`. Zum Umziehen oder Sichern reicht der Export unten auf der Seite.

Die Broadcast-Adresse eines Geräts wird automatisch aus dem Netz seiner IP bestimmt; nur bei Sonderfällen im
Gerät unter „Erweitert“ eintragen. Ist keine IP bekannt, gilt `bcastip`, ohne Einstellung die Broadcast-Adresse des
Suchsegments (wenn genau eins eingetragen ist), sonst `255.255.255.255:9`. Ist am Gerät eine Netzwerkschnittstelle
eingetragen, verlässt das Paket den Rechner unter Linux auch wirklich über diese Schnittstelle.

## Sicherheit

- **Anmeldung**: `auth.username`/`auth.password` (HTTP Basic, im Internet nur hinter HTTPS) und/oder `auth.api_token`
  für Automationen.
- **Öffentliche Wecklinks** (`auth.public_wake`): Mit Anmeldung bleiben `/wake/<Name>` und die Weckseite ohne Login
  erreichbar. Damit niemand Geräte über erratene Namen weckt, verlangt `auth.wake_keys` zusätzlich einen Schlüssel
  je Gerät – „Wecklink kopieren“ hängt ihn an (`/wake/NAS?key=…`), im Gerätedialog lässt er sich erneuern. Ohne
  Anmeldung kann ein Gerät höchstens alle 10 s geweckt werden.
- **DNS-Rebinding**: Ohne Anmeldung nimmt wolweb nur Aufrufe unter IP-Adressen, Namen ohne Punkt (`nas`), lokalen
  Endungen (`.lan`, `.local`, `.home`, `.internal`, `.home.arpa`, `.fritz.box` …) und den Namen aus
  `allowed_hosts` an (`"wol.example.com"`, `"*.example.com"`, `"*"` schaltet die Prüfung ab). Sonst könnte eine
  fremde Webseite ihren Namen auf die IP von wolweb umbiegen und die API im Browser fernsteuern. Mit Anmeldung wird
  nur geprüft, wenn `allowed_hosts` gesetzt ist.

## API

Alle ändernden Aufrufe brauchen den Kopf `X-WolWeb: 1` (Schutz gegen Cross-Site-Requests) oder ein Bearer-Token.

| Methode | Pfad | |
|---|---|---|
| GET | `/api/v1/info` | Einstellungen, lokale Netze |
| GET / POST | `/api/v1/devices` | Liste (mit Status) / anlegen |
| PUT / DELETE | `/api/v1/devices/{id}` | ändern (`updated_at` mitschicken schützt vor Überschreiben) / löschen |
| POST | `/api/v1/devices/{id}/wake` | Magic Packet senden |
| GET | `/api/v1/devices/{id}/status` | sofort prüfen |
| POST | `/api/v1/devices/{id}/wake-key` | neuen Weckschlüssel erzeugen |
| GET | `/api/v1/history` | Weckverlauf, neueste zuerst (`?limit=`) |
| GET / POST | `/api/v1/export`, `/api/v1/import` | Geräte als JSON herunterladen / aus einer Datei übernehmen |
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
