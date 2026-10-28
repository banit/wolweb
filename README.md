# wolweb

Wake on LAN mit Weboberfläche – Neufassung von [sameerdhoot/wolweb](https://github.com/sameerdhoot/wolweb) in Go,
als einzelne Datei ohne externe Abhängigkeiten im Browser.

*English: see [below](#english).*

![Übersicht mit Gruppen, Online-Status und nächster geplanter Weckzeit](docs/screenshots/overview-light.png)

<table>
<tr>
<td width="50%"><img src="docs/screenshots/wake-waiting.png" alt="Weckseite: Magic Packet gesendet, wartet auf den Start"></td>
<td width="50%"><img src="docs/screenshots/wake-online.png" alt="Weckseite: Gerät ist online"></td>
</tr>
<tr>
<td colspan="2" align="center"><sub>Fester Wecklink auf dem Handy: prüft, weckt und bestätigt den Start</sub></td>
</tr>
<tr>
<td><img src="docs/screenshots/discovery.png" alt="Netzwerksuche mit neuen Geräten"></td>
<td><img src="docs/screenshots/device-dialog.png" alt="Gerätedialog im Dunkelmodus"></td>
</tr>
<tr>
<td><img src="docs/screenshots/history.png" alt="Weckverlauf"></td>
<td><img src="docs/screenshots/overview-en.png" alt="Übersicht auf Englisch"></td>
</tr>
<tr>
<td align="center"><sub>Netzwerksuche · Verlauf</sub></td>
<td align="center"><sub>Gerätedialog (dunkel) · englische Oberfläche</sub></td>
</tr>
</table>

- **Übersicht** mit Online-Status jedes Geräts (Ping und TCP-Ports), Suche und Filter, hell/dunkel – Änderungen
  erscheinen sofort in jedem offenen Fenster (Server-Sent Events)
- **Feste Wecklinks** `/wake/<Name>`: im Browser eine Bestätigungsseite, die das Gerät prüft, das Magic Packet
  sendet und den Start verfolgt, bis das Gerät wirklich online ist
- **Zuverlässiges Wecken**: Das Magic Packet geht dreimal innerhalb einer Sekunde an Port 9 und 7; antwortet ein
  Gerät mit IP nach 45 Sekunden noch nicht, schickt wolweb es von selbst noch einmal – auch bei Wecklinks,
  Gruppen und Zeitplänen, wenn niemand mehr zuschaut
- **Netzwerksuche**: findet eingeschaltete Geräte in freigegebenen Segmenten (IP, MAC, Hostname, Hersteller) –
  per Hand oder regelmäßig im Hintergrund; neue Geräte mit einem Klick übernehmen oder ignorieren
- **Einstellungsseite** mit Übersicht der Netze und VLANs (an welchen wolweb hängt, Geräte je Netz); Änderungen
  gelten ohne Neustart
- **Gruppen** („Homelab“, „Büro“) gemeinsam wecken, auch per festem Gruppen-Wecklink; **Zeitpläne** wie
  `Mo-Fr 07:30` wecken Geräte automatisch
- **Benachrichtigungen** per Webhook (JSON) oder ntfy aufs Handy, wenn ein Gerät online/offline geht oder
  geweckt wird; **Prometheus-Metriken** unter `/metrics`
- **Verlauf** jedes Weckversuchs (wer, wann, woher, Ergebnis), **Export/Import** der Geräte als JSON
- **Deutsch und Englisch**: Die Oberfläche folgt der Sprache des Browsers und lässt sich oben rechts umschalten;
  auch die Meldungen der API kommen in der gewählten Sprache
- Übernimmt die `devices.json` und die Einstellungen des alten wolweb

## Geräte

Pflicht sind nur Name und MAC-Adresse. Die IP-Adresse braucht es für den Online-Status (sonst nimmt wolweb die IP
aus der Netzwerksuche).

| Feld | Wofür |
|---|---|
| Gruppe | Geräte mit gleicher Gruppe stehen zusammen und lassen sich gemeinsam wecken |
| Zeitplan | automatische Weckzeiten, z. B. `Mo-Fr 07:30; Sa,So 09:00`, `werktags 6:45`, `täglich 22:00` (englische Schreibweise wie `Mon-Fri 07:30` geht auch; gespeichert wird einheitlich die deutsche Form, angezeigt in der gewählten Sprache) |
| *Erweitert:* Broadcast-Adresse | Ziel des Magic Packets, nur für Sonderfälle (sonst automatisch) |
| *Erweitert:* Netzwerkschnittstelle | Paket über genau diese Schnittstelle senden (Rechner mit mehreren Netzen) |
| *Erweitert:* Ports | eigene Ports für die Online-Prüfung statt der allgemeinen Liste |
| *Erweitert:* SecureOn-Passwort | 6 Byte, falls die Netzwerkkarte ein Passwort im Magic Packet verlangt |
| *Erweitert:* Notiz | freier Text auf der Karte |

Zeitpläne gelten in der Ortszeit des Rechners bzw. Containers (`TZ`, in `docker-compose.yml` voreingestellt).
Die Karte zeigt die nächste geplante Weckzeit.

## Feste Wecklinks

| Aufruf | Ergebnis |
|---|---|
| `http://host:8089/wake/NAS` im Browser | Weckseite mit Fortschritt: *Gerät prüfen → Magic Packet senden → Auf Start warten → Online* |
| `curl http://host:8089/wake/NAS` | weckt sofort, Antwort JSON `{"success":true,"message":…}` (wie im Original) |
| `curl "http://host:8089/wake/NAS?wait=120"` | weckt und wartet bis zu 120 s, bis das Gerät antwortet (`"online": true/false`; fehlt, wenn keine IP bekannt ist) |
| `…/wake/NAS?format=html` / `?format=json` | Ausgabe erzwingen |
| `http://host:8089/wake-group/Homelab` | ganze Gruppe: im Browser eine Seite mit dem Start jedes Geräts, sonst JSON (auch `?wait=`) |

Der Name ist nicht von Groß-/Kleinschreibung abhängig. Die Bestätigung „online“ gibt es nur, wenn eine IP bekannt
ist (eingetragen oder aus der Netzwerksuche). Läuft das Gerät schon, sagt die Seite das und sendet nichts.
`HEAD`-Anfragen (Linkprüfer, Vorschauen in Messengern) wecken nie.

Tipp: Die Weckseite auf dem Handy „Zum Home-Bildschirm“ hinzufügen – dann weckt ein Tipp aufs Symbol das Gerät.
Die Übersicht selbst lässt sich ebenso als App ablegen.

Online gilt ein Gerät, wenn es auf Ping antwortet oder einer der Ports (`status.ports`) eine Verbindung annimmt **oder
ablehnt** – eine Ablehnung kommt nur von einem laufenden Betriebssystem. Das gilt für Geräte im eigenen Netzsegment;
hinter einem Router zählt nur eine angenommene Verbindung oder Ping, weil dort auch eine Firewall stellvertretend
ablehnen kann.

Geprüft wird zuerst per Ping und mit der Methode, die beim letzten Mal geklappt hat; die übrigen Ports kommen erst
0,3 s später dran. Ein Gerät, das auf Ping antwortet, bekommt so keine einzige TCP-Verbindung (kein Rauschen im
SSH-Log). Pro Gerät lassen sich unter „Erweitert“ eigene Ports eintragen, z. B. `32400` für einen Plex-Server.

## Netzwerksuche

Die Netze stehen in den [Einstellungen](#einstellungen) unter „Netze und VLANs“ (oder in `discovery.networks` bzw.
`WOLWEB_NETWORKS`), z. B. `192.168.1.0/24`. Gesucht wird nur in Netzen, an denen der Rechner direkt hängt – ARP und
Wake on LAN gehen nicht über Router. Für weitere VLANs siehe [Mehrere Netze und VLANs](#mehrere-netze-und-vlans).

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

Fertige Images für amd64, arm64 (Raspberry Pi 4/5) und armv7 baut GitHub bei jedem Release-Tag nach
`ghcr.io/banit/wolweb` (siehe `docker-compose.yml`). Selbst für mehrere Plattformen bauen:
`docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7 .`

### Direkt / Proxmox-LXC

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=1.1.0" -o wolweb .
```

Nach `/opt/wolweb` kopieren, `config.example.json` als `config.json` anpassen, `deploy/wolweb.service` nach
`/etc/systemd/system/` (Benutzer `wolweb` anlegen, `data/` beschreibbar machen), dann
`systemctl enable --now wolweb`.

## Update von 1.0.x / 1.1.x

Geräte und Einstellungen werden beim ersten Start automatisch übernommen. Selbst tun musst du nur dann etwas, wenn
du wolweb **ohne Anmeldung über eine eigene Domain** aufrufst, etwa hinter einem Reverse-Proxy
(`https://wol.example.com`): Seit 1.1 nimmt wolweb ohne Anmeldung nur IP-Adressen und lokale Namen (`nas`,
`*.lan`, `*.local`, `*.fritz.box` …) an, als Schutz vor DNS-Rebinding. Unter der Domain erscheint dann eine Seite,
die genau sagt, was einzutragen ist. Am einfachsten öffnest du wolweb einmal über seine IP-Adresse
(`http://192.168.1.5:8089`) und erlaubst den Namen in den Einstellungen unter „Sicherheit“ – er steht dort schon
bereit. Alternativ in `config.json` `"allowed_hosts": ["wol.example.com"]` bzw. in Docker
`WOLWEB_ALLOWED_HOSTS=wol.example.com`. Beim Start steht ein entsprechender Hinweis im Log.

**Sicherung und Weg zurück:** Bevor eine neue Version die Daten zum ersten Mal schreibt, legt sie eine Kopie an:
`data/sicherung-<alte Version>-<Datum>/` (etwa `sicherung-1.0.x-20261028-160512/` mit `devices.json`,
`discovered.json` und – ab 1.1 – `state.json` und `settings.json`). Das passiert einmal je Versionswechsel. Eine
ältere Version kann die neuen Dateien zwar lesen, verwirft beim nächsten Speichern aber, was sie nicht kennt
(SecureOn, Weckschlüssel, Gruppen, Zeitpläne, eigene Ports, Verlauf). Zurück geht es so: wolweb stoppen, die
Dateien aus dem Sicherungsordner nach `data/` kopieren, Dateien, die in der Sicherung fehlen (bei 1.0.x
`state.json` und `settings.json`), aus `data/` löschen, dann die alte Version starten. Was seit dem Update
geändert wurde, fehlt danach.

## Einstellungen

Das Zahnrad oben rechts öffnet die Einstellungsseite (`/settings`). Dort lassen sich die Netze und VLANs,
Netzwerksuche, Online-Prüfung, Wartezeiten, Benachrichtigungen (mit „Testnachricht senden“), öffentliche Wecklinks,
erlaubte Hostnamen und die Standardsprache ändern. Änderungen gelten sofort, ohne Neustart.

- **Reihenfolge** (das Spätere gewinnt): Standardwerte, `config.json`, die Einstellungsseite, Umgebungsvariablen.
- **Gespeichert** wird in `data/settings.json` – im Datenverzeichnis, weil das immer beschreibbar ist (`config.json`
  ist in Docker oft schreibgeschützt eingebunden). Dort steht nur, was von `config.json` abweicht; spätere
  Änderungen an `config.json` wirken also weiter, solange man denselben Wert nicht auch auf der Seite geändert hat.
  „Auf config.json zurücksetzen“ löscht die Datei wieder.
- **Gesperrt**: Was per Umgebungsvariable gesetzt ist (in Docker etwa `WOLWEB_NETWORKS`), zeigt die Seite
  ausgegraut mit dem Namen der Variable. Wer es lieber auf der Seite pflegt, nimmt die Variable heraus.
- **Nicht auf der Seite**: Adresse, Port, Pfad, Datenverzeichnis, Nur-Lesen und die Zugangsdaten. Über die Seite
  könnte man sich sonst aussperren – oder ohne Anmeldung einfach eine setzen.
- Ohne Anmeldung kann jeder im Netz die Einstellungen sehen und ändern; die Seite weist darauf hin. Im
  Nur-Lesen-Modus lässt sie sich nur ansehen.

![Einstellungen: Netze und VLANs mit Schnittstellen des Rechners](docs/screenshots/settings.png)

## Mehrere Netze und VLANs

Magic Packets sind Broadcasts, und die Netzwerksuche fragt per ARP – beides geht nicht über Router. wolweb braucht
deshalb in jedem Netz, in dem es wecken und suchen soll, **eine eigene Schnittstelle**: meist ein zusätzliches
Interface mit VLAN-Tag auf dem Rechner, auf dem wolweb läuft. Ist es angelegt, erscheint es in den Einstellungen
unter „Schnittstellen dieses Rechners“; „Übernehmen“ trägt das Netz ein, dazu ein Name („IoT“) und die VLAN-ID.
Geräte darin weckt wolweb über die richtige Schnittstelle (die Broadcast-Adresse folgt aus der IP des Geräts), und
die Suche findet sie. Für Geräte ohne IP schlägt der Gerätedialog unter „Erweitert → Broadcast-Adresse“ die
Broadcasts der eingetragenen Netze vor.

Voraussetzung: Der Switch-Port bzw. die Bridge des Rechners reicht das VLAN getaggt durch (Trunk), und wolweb hat im
VLAN eine eigene, feste Adresse (außerhalb des DHCP-Bereichs oder per DHCP-Reservierung). **Kein Gateway** auf den
zusätzlichen Schnittstellen eintragen – die Standardroute bleibt im Hauptnetz.

**Proxmox (LXC):** Dem Container eine zweite Netzwerkkarte mit VLAN-Tag geben – in der Oberfläche unter
*Container → Netzwerk → Hinzufügen* (Bridge `vmbr0`, VLAN-Tag `20`, IPv4 statisch, Gateway leer) oder auf dem Host:

```sh
pct set 105 -net1 name=eth1,bridge=vmbr0,tag=20,ip=192.168.20.5/24
```

Die Bridge muss „VLAN aware“ sein (*Host → System → Netzwerk → vmbr0 → VLAN aware*, bzw. `bridge-vlan-aware yes` in
`/etc/network/interfaces`). Bei einer VM entsprechend eine weitere Netzwerkkarte mit VLAN-Tag hinzufügen.

**Debian, Raspberry Pi OS, Ubuntu (direkt oder als Docker-Host):** VLAN-Interface anlegen. Zum Ausprobieren:

```sh
sudo ip link add link eth0 name eth0.20 type vlan id 20
sudo ip addr add 192.168.20.5/24 dev eth0.20
sudo ip link set eth0.20 up
```

Dauerhaft mit NetworkManager (Raspberry Pi OS ab Bookworm, Ubuntu Desktop):

```sh
sudo nmcli con add type vlan con-name vlan20 dev eth0 id 20 \
  ipv4.method manual ipv4.addresses 192.168.20.5/24 ipv4.never-default yes ipv6.method disabled
```

mit ifupdown (Debian, Paket `vlan`) in `/etc/network/interfaces.d/vlan20`:

```
auto eth0.20
iface eth0.20 inet static
    address 192.168.20.5/24
```

mit netplan (Ubuntu Server) in `/etc/netplan/60-vlan20.yaml`, danach `sudo netplan apply`:

```yaml
network:
  version: 2
  vlans:
    vlan20:
      id: 20
      link: eth0
      addresses: [192.168.20.5/24]
```

**Docker:** Mit `network_mode: host` (wie in `docker-compose.yml`) sieht wolweb alle Schnittstellen des Hosts –
das VLAN-Interface also wie oben auf dem Host anlegen, am Container ändert sich nichts. Ohne Host-Netz geht es mit
je einem macvlan-Netz pro VLAN (`docker network create -d macvlan -o parent=eth0.20 --subnet 192.168.20.0/24 vlan20`),
das ist aber umständlicher. Docker Desktop (Windows, macOS) kann beides nicht.

**Absichern:** Eine Schnittstelle in einem abgeschotteten Netz (IoT, Gäste) macht wolweb auch von dort erreichbar.
Mit `host` in `config.json` (bzw. `WOLWEB_HOST`) die Weboberfläche nur an die Adresse im Hauptnetz binden, z. B.
`"host": "192.168.1.5"` – Wecken und Suche im VLAN funktionieren trotzdem.

Geht keine eigene Schnittstelle, zeigt die Einstellungsseite das Netz als „nicht direkt angeschlossen“ und weckt
Geräte darin per gerichtetem Broadcast (z. B. `192.168.20.255:9`). Ankommen wird er nur, wenn der Router ihn ins
VLAN weiterleitet – bei OPNsense und pfSense etwa mit dem Plugin „UDP Broadcast Relay“; die meisten Heimrouter
können das nicht, und suchen lässt sich dort ohnehin nicht.

## Konfiguration

`config.json` (siehe `config.example.json`) – alles hat Standardwerte, die Datei darf fehlen. Was auf der
[Einstellungsseite](#einstellungen) geändert wurde, gilt vor der Datei; Umgebungsvariablen gehen allem vor.

| Schlüssel | Variable | Standard | Bedeutung |
|---|---|---|---|
| `host`, `port` | `WOLWEBHOST`, `WOLWEBPORT` | `0.0.0.0`, `8089` | Adresse der Weboberfläche |
| `vdir` | `WOLWEBVDIR` | `/` | Unterpfad, z. B. `/wolweb` hinter einem Reverse-Proxy |
| `bcastip` | `WOLWEBBCASTIP` | automatisch | Standardziel, wenn weder Gerät noch Netz eins liefern |
| `read_only` | `WOLWEBREADONLY` | `false` | nur wecken, nichts ändern |
| `language` | `WOLWEB_LANGUAGE` | `de` | `de` oder `en`: Sprache, wenn weder Umschalter noch Browser eine nennen (auch für Benachrichtigungen) |
| `data_dir` | `WOLWEB_DATA_DIR` | `data` | Geräte (`devices.json`), Laufzeitdaten (`state.json`), Suchergebnisse (`discovered.json`) und Änderungen der Einstellungsseite (`settings.json`) |
| `discovery.networks` | `WOLWEB_NETWORKS` | – | Netze (CIDR, Variable kommagetrennt), auch mit Namen: `{"cidr": "192.168.20.0/24", "name": "IoT", "vlan": 20, "scan": false}` – `scan: false` heißt anzeigen, aber nicht durchsuchen |
| `discovery.interval` | `WOLWEB_SCAN_INTERVAL` | `30m` | automatische Suche, `0` = aus (mindestens `1m`) |
| `discovery.max_hosts` | – | `1024` | größtes erlaubtes Segment |
| `status.interval` | `WOLWEB_STATUS_INTERVAL` | `30s` | Online-Prüfung aller Geräte, `0` = aus (mindestens `5s`) |
| `status.ports` | – | 22, 80, 443, 445, … | Ports für die Online-Prüfung |
| `status.wake_timeout` | `WOLWEB_WAKE_TIMEOUT` | `3m` | so lange wartet die Weckseite (`5s` bis `10m`) |
| `status.wake_retry` | `WOLWEB_WAKE_RETRY` | `45s` | antwortet ein geweckter Rechner so lange nicht, geht das Paket einmal erneut raus; `0` = nie |
| `auth.username/password` | `WOLWEB_USER`, `WOLWEB_PASSWORD` | – | Anmeldung (HTTP Basic) |
| `auth.api_token` | `WOLWEB_TOKEN` | – | `Authorization: Bearer …` für Automationen |
| `auth.public_wake` | `WOLWEB_PUBLIC_WAKE` | `true` | Wecklinks funktionieren ohne Anmeldung |
| `auth.wake_keys` | `WOLWEB_WAKE_KEYS` | `false` | Wecklinks ohne Anmeldung nur mit dem Schlüssel des Geräts (`?key=…`) |
| `allowed_hosts` | `WOLWEB_ALLOWED_HOSTS` | – | weitere Hostnamen, unter denen wolweb erreichbar ist (siehe Sicherheit) |
| `notify.webhook` | `WOLWEB_WEBHOOK` | – | URL, die jedes Ereignis als JSON per POST bekommt |
| `notify.ntfy` | `WOLWEB_NTFY` | – | ntfy-Thema, z. B. `https://ntfy.sh/mein-geheimes-thema` |
| `notify.events` | `WOLWEB_NOTIFY_EVENTS` | alle | Auswahl aus `online`, `offline`, `wake`, `wake_failed` |

`devices.json` enthält nur, was man selbst einträgt; was wolweb beobachtet (zuletzt geweckt, zuletzt online, die
letzten 500 Weckversuche) steht in `state.json`. Zum Umziehen oder Sichern reicht der Export unten auf der Seite.

Die Broadcast-Adresse eines Geräts wird automatisch aus dem Netz seiner IP bestimmt; nur bei Sonderfällen im
Gerät unter „Erweitert“ eintragen. Ist keine IP bekannt, gilt `bcastip`, ohne Einstellung die Broadcast-Adresse des
Suchsegments (wenn genau eins eingetragen ist), sonst `255.255.255.255:9`. Ist am Gerät eine Netzwerkschnittstelle
eingetragen, verlässt das Paket den Rechner unter Linux auch wirklich über diese Schnittstelle.

Ein Magic Packet ist ein einzelnes UDP-Paket ohne Bestätigung – geht es verloren (WLAN-Brücke, ein Switch-Port im
Energiesparmodus), merkt das niemand. wolweb schickt es deshalb dreimal im Abstand von 0,4 Sekunden, und zwar an
Port 9 und Port 7, auf die Netzwerkkarten je nach Hersteller hören (ein anderer eingetragener Port, etwa für eine
Weiterleitung im Router, bleibt allein). Antwortet ein Gerät mit IP-Adresse nach `status.wake_retry` (45 s) noch
nicht, geht das Paket einmal erneut raus; im Verlauf steht das als „automatische Wiederholung“. Wird das Gerät in
der Zwischenzeit noch einmal geweckt, zählt die Frist von vorn.

## Benachrichtigungen und Metriken

Mit `notify.webhook` bekommt eine URL jedes Ereignis als JSON, z. B. für einen Home-Assistant-Webhook oder Node-RED:

```json
{"event":"online","device":"NAS","id":"3fa2b1c4d5e6","ip":"192.168.1.20","message":"NAS ist online (icmp)","time":"…"}
```

Mit `notify.ntfy` kommt eine Push-Nachricht aufs Handy (App „ntfy“, Thema abonnieren). Gemeldet werden nur echte
Wechsel online ↔ offline, nicht das erste Ergebnis nach dem Start.

wolweb schickt Benachrichtigungen genau an die eingetragene Adresse – auch an Server im eigenen Netz, etwa ein
selbst gehostetes ntfy oder Home Assistant. **Weiterleitungen folgt es nicht**: Antwortet das Ziel mit einer
Umleitung (3xx), geht die Nachricht nicht weiter, sondern es steht ein Fehler im Log (und bei „Testnachricht
senden“). Sonst könnte ein umgeleiteter Server die Nachricht an ein Ziel schicken lassen, das niemand eingetragen
hat. Abhilfe: die endgültige Adresse direkt eintragen (z. B. mit `https://` statt `http://`).

`/metrics` liefert Kennzahlen im Prometheus-Format (`wolweb_device_up`, `wolweb_device_rtt_seconds`,
`wolweb_device_last_seen_timestamp_seconds`, `wolweb_wakes_total`, `wolweb_discovery_found` …). Mit Anmeldung
braucht auch der Abruf sie – in Prometheus `authorization: {credentials: <api_token>}`.

## Sicherheit

- **Anmeldung**: `auth.username`/`auth.password` (HTTP Basic, im Internet nur hinter HTTPS) und/oder `auth.api_token`
  für Automationen.
- **Öffentliche Wecklinks** (`auth.public_wake`): Mit Anmeldung bleiben `/wake/<Name>` und die Weckseite ohne Login
  erreichbar. Damit niemand Geräte über erratene Namen weckt, verlangt `auth.wake_keys` zusätzlich einen Schlüssel
  je Gerät – „Wecklink kopieren“ hängt ihn an (`/wake/NAS?key=…`), im Gerätedialog lässt er sich erneuern.
- **Kein doppeltes Wecken**: Jedes Gerät bekommt höchstens alle 10 s einen Weckruf – mit und ohne Anmeldung, über
  Wecklink, Gruppenlink, Weckseite und API gemeinsam. Ein zweiter Aufruf in dieser Zeit (zweimal getippt, Seite neu
  geladen) ist kein Fehler: Er meldet „wird bereits geweckt“ (`"already": true`) und die Seite verfolgt den
  laufenden Start, ohne erneut zu senden. Zeitpläne und die automatische Wiederholung werden nie gebremst.
- **Wecklinks von fremden Seiten**: Ein Wecklink weckt beim Öffnen – mit Absicht, auch wenn er auf einer anderen
  Seite steht (Home-Assistant-Dashboard, Glance, Lesezeichenseite). Das heißt aber auch: Jede Webseite, die du
  besuchst, kann deinen Browser zu `/wake/<Name>` oder `/wake-group/<Name>` schicken, und die ausgelieferte
  Weckseite weckt dann. Mehr als Wecken ist so nicht möglich, und `HEAD` (Linkvorschauen in Messengern, Linkprüfer)
  weckt nie. Wer auch das verhindern will, schaltet die Anmeldung samt `auth.wake_keys` ein: Dann braucht jeder
  Link – Gerät wie Gruppe gleichermaßen – den Schlüssel, den „Wecklink kopieren“ anhängt, und Namen lassen sich
  nicht erraten.
- **DNS-Rebinding**: Ohne Anmeldung nimmt wolweb nur Aufrufe unter IP-Adressen, Namen ohne Punkt (`nas`), lokalen
  Endungen (`.lan`, `.local`, `.home`, `.internal`, `.home.arpa`, `.fritz.box` …) und den Namen aus
  `allowed_hosts` an (`"wol.example.com"`, `"*.example.com"`, `"*"` schaltet die Prüfung ab). Sonst könnte eine
  fremde Webseite ihren Namen auf die IP von wolweb umbiegen und die API im Browser fernsteuern. Mit Anmeldung wird
  nur geprüft, wenn `allowed_hosts` gesetzt ist.

## Sprache

Die Oberfläche gibt es auf Deutsch und Englisch. Gewählt wird in dieser Reihenfolge: `?lang=de|en` in der Adresse,
der Umschalter oben rechts (Cookie `wolweb_lang`), die Sprache des Browsers (`Accept-Language`), sonst `language`.
Das gilt auch für die API – Automationen bekommen Meldungen auf Englisch mit `Accept-Language: en`. Fehlerantworten
nennen zusätzlich das betroffene Feld (`"field": "mac"`). Die Texte stehen in `internal/i18n/de.json` und
`en.json`; ein Test prüft, dass beide dieselben Schlüssel haben und alles Benutzte vorhanden ist. Das Protokoll
(Log) bleibt deutsch.

## Barrierefreiheit

wolweb soll sich auch mit eingeschränkter Feinmotorik, nur mit der Tastatur oder mit einem Screenreader bedienen
lassen:

- **Einfache Ansicht** (Knopf mit dem großen Schalter oben rechts): nur Name, Status und ein großer Weckknopf je
  Gerät, nichts zum Bearbeiten oder Verschieben. Die Wahl merkt sich der Browser; ein Link mit `?view=simple`
  schaltet sie ein (etwa als Lesezeichen, das man jemandem einrichtet), `?view=normal` wieder aus.
- **Große Ziele auf Touchscreens**: Auf Geräten mit Touch sind alle Knöpfe, Felder und Filter mindestens
  44 × 44 Pixel groß und weiter auseinander – auch ein unruhiger Finger trifft.
- **Ein Tipp genügt**: Der Wecklink eines Geräts (`/wake/<Name>`) weckt schon beim Öffnen. Als Symbol auf dem
  Startbildschirm des Handys ist das ein einziger Tipp, ohne die Übersicht zu bedienen.
- **Tastatur**: alles per Tab erreichbar, sichtbarer Fokus, Sprunglink „Zu den Geräten“, `/` springt in die
  Suche, Escape schließt Dialoge und die Suchleiste; solange die Leiste offen ist, bleibt der Fokus darin.
- **Screenreader**: Knöpfe nennen das Gerät, zu dem sie gehören; Weck- und Gruppenseite sagen die Phasen an
  (sendet, wartet, online); Filter melden, welcher aktiv ist.
- **Kontrast** nach WCAG AA (4,5:1) in hell und dunkel; Animationen entfallen bei „Bewegung reduzieren“.

Die Browsertests (siehe Entwicklung) prüfen das bei jedem Push: [axe-core](https://github.com/dequelabs/axe-core)
nach WCAG 2.2 AA auf allen Seiten und Dialogen in hell und dunkel, die Tastaturbedienung und die Größe aller
Touch-Ziele auf einem Handy.

## API

Alle ändernden Aufrufe brauchen den Kopf `X-WolWeb: 1` (Schutz gegen Cross-Site-Requests) oder ein Bearer-Token.

| Methode | Pfad | |
|---|---|---|
| GET | `/api/v1/info` | Einstellungen, lokale Netze |
| GET | `/api/v1/events` | Live-Ereignisse (Server-Sent Events): `status`, `devices`, `scan` |
| GET / POST | `/api/v1/devices` | Liste (mit Status) / anlegen |
| PUT / DELETE | `/api/v1/devices/{id}` | ändern (`updated_at` mitschicken schützt vor Überschreiben) / löschen |
| POST | `/api/v1/devices/{id}/wake` | Magic Packet senden |
| GET | `/api/v1/devices/{id}/status` | sofort prüfen |
| POST | `/api/v1/devices/{id}/wake-key` | neuen Weckschlüssel erzeugen |
| GET | `/api/v1/groups` | Gruppen mit Mitgliedern (und Schlüssel des Gruppen-Wecklinks) |
| POST | `/api/v1/groups/{name}/wake` | alle Geräte einer Gruppe wecken |
| GET | `/api/v1/history` | Weckverlauf, neueste zuerst (`?limit=`) |
| GET / POST | `/api/v1/export`, `/api/v1/import` | Geräte als JSON herunterladen / aus einer Datei übernehmen |
| GET | `/api/v1/discovery` | letzte Suche und Treffer |
| POST | `/api/v1/discovery/scan`, `/cancel` | Suche starten / abbrechen |
| POST | `/api/v1/discovery/{mac}/ignore` | `{"ignored":true}` |
| GET | `/wake/{name}`, `/health`, `/data/get` | wie im Original |
| GET / POST | `/wake-group/{name}` | Gruppen-Wecklink |
| GET | `/metrics` | Kennzahlen für Prometheus |

## Entwicklung

```sh
go test ./...
go run . -c config.json
```

Die Oberfläche liegt in `internal/web/` (Vorlagen und statische Dateien, eingebettet per `go:embed`).
Herstellerliste aktualisieren: `sh tools/gen-oui.sh`.

Browsertests (Playwright mit Chromium, braucht Go und Node.js) liegen in `e2e/`. Sie bauen wolweb selbst, starten
es mit leerem Datenverzeichnis auf Port 8399 (Pakete gehen nur an 127.0.0.1) und prüfen Übersicht, Gerätedialog,
Suche und Filter, Sprache, einfache Ansicht, Weck- und Gruppenseite mit allen Ausgängen samt automatischer
Wiederholung sowie die Barrierefreiheit:

```sh
cd e2e
npm ci
npx playwright install chromium
npx playwright test
```

GitHub Actions prüft jeden Push und Pull Request (gofmt, `go vet`, `go test -race`, Builds für Linux amd64/arm64/
armv7, Windows, macOS, Browsertests, Docker-Image für drei Plattformen). Ein Tag `v*` (`git tag v1.1.0 && git push --tags`)
erzeugt ein Release mit fertigen Programmen und das Image auf ghcr.io – das Paket danach einmal unter
„Packages“ auf öffentlich stellen.

---

## English

**wolweb** is a Wake on LAN web app – a Go rewrite of [sameerdhoot/wolweb](https://github.com/sameerdhoot/wolweb):
a single binary (or a 23 MB Docker image) with no external dependencies in the browser. The interface is available
in **English and German**; it follows the browser language and can be switched in the top right corner.

### Features

- **Overview** with the online status of every device (ping and TCP ports), search and filters, light/dark mode –
  changes appear instantly in every open window (Server-Sent Events)
- **Fixed wake links** `/wake/<name>`: in a browser a confirmation page that checks the device, sends the magic
  packet and follows the start until the device is really online; `curl` and Home Assistant get JSON
  (`?wait=120` waits until the device responds)
- **Reliable waking**: the magic packet goes out three times within one second to ports 9 and 7; if a device with an
  IP address still does not respond after 45 seconds (`status.wake_retry`), wolweb sends it once more by itself –
  for wake links, groups and schedules too, when nobody is watching
- **Network scan**: finds devices that are switched on in the configured segments (IP, MAC, host name, vendor) –
  on demand or periodically; add new devices with one click or ignore them
- **Settings page** with an overview of networks and VLANs (which ones wolweb is connected to, devices per network);
  changes apply without a restart
- **Groups** ("Homelab", "Office") wake together, also via a fixed group wake link `/wake-group/<name>`;
  **schedules** such as `Mon-Fri 07:30; Sat,Sun 09:00` wake devices automatically (German spellings work too;
  devices.json always stores the canonical German form, the UI shows it in the chosen language)
- **Notifications** via webhook (JSON) or ntfy push when a device goes online/offline or is woken – sent exactly to
  the configured address (servers on your LAN included); redirects (3xx) are not followed but logged as an error, so
  a redirecting server cannot divert messages to a target nobody configured; **Prometheus metrics** at `/metrics`
- **History** of every wake attempt (who, when, from where, result), **export/import** of devices as JSON
- **Security**: optional login (HTTP Basic, API token), per-device keys for public wake links, no double waking (one
  wake-up call per device every 10 s on all paths; a second call reports `"already": true`). A wake link wakes when
  opened – on purpose, also from dashboards on other sites; any website you visit could therefore send your browser
  to `/wake/<name>` or `/wake-group/<name>` (`HEAD` never wakes). To prevent that, enable the login together with
  `auth.wake_keys`: every link, device or group alike, then needs its key,
  protection against DNS rebinding
- Per-device ports for the online check, SecureOn passwords, sending via a specific network interface,
  home-screen icons (web app manifest)
- **Accessibility**: a **simple view** with only name, status and one large wake button per device (toggle at the
  top right, or `?view=simple` in a bookmark), touch targets of at least 44 × 44 px on touch screens, full keyboard
  operation, screen reader announcements on the wake pages, WCAG AA contrast – checked with axe-core in CI.
  A device's wake link wakes it as soon as it is opened, so a home-screen icon is a single tap
- Imports the `devices.json` and settings of the original wolweb

### Quick start (Docker)

```sh
docker compose up -d --build
```

`docker-compose.yml` uses `network_mode: host` (required for broadcasts and ARP) and grants only `NET_RAW`. Data is
stored in `./data`; the folder must belong to root (let Docker create it, or `sudo chown -R root:root data`). To import
an old `devices.json`, put it at `./data/legacy-devices.json` before the first start. Release tags publish
multi-arch images (amd64, arm64, armv7) to `ghcr.io/banit/wolweb`. Docker Desktop (Windows/macOS) has no real host
network – waking and scanning do not work there.

Without Docker: `CGO_ENABLED=0 go build -o wolweb .`, adapt `config.example.json` as `config.json`, and use
`deploy/wolweb.service` for systemd (it grants `CAP_NET_RAW` for ping and ARP scans).

### Updating from 1.0.x / 1.1.x

Devices and settings are taken over automatically on the first start. You only need to act if you open wolweb
**without login via your own domain**, e.g. behind a reverse proxy (`https://wol.example.com`): since 1.1 wolweb
only accepts IP addresses and local names (`nas`, `*.lan`, `*.local`, `*.fritz.box` …) without login, as protection
against DNS rebinding. The domain then shows a page that says exactly what to add. The easiest way: open wolweb once
via its IP address (`http://192.168.1.5:8089`) and allow the name in the settings under “Security” – it is already
offered there. Alternatively `"allowed_hosts": ["wol.example.com"]` in `config.json` or
`WOLWEB_ALLOWED_HOSTS=wol.example.com` in Docker. A hint is logged at start-up.

**Backup and going back:** before a new version writes the data for the first time, it makes a copy in
`data/sicherung-<old version>-<date>/` (e.g. `sicherung-1.0.x-20261028-160512/`), once per version change. An older
version can read the new files but drops what it does not know (SecureOn, wake keys, groups, schedules, custom
ports, history) on its next save. To go back: stop wolweb, copy the files from the backup folder into `data/`,
delete files from `data/` that the backup does not contain (for 1.0.x `state.json` and `settings.json`), and start
the old version. Changes made since the update are lost.

### Settings page

The gear at the top right opens the settings page (`/settings`): networks and VLANs, network scan, online check,
wait times, notifications (with “Send test message”), public wake links, allowed host names and the default language.
Changes apply immediately, without a restart. They are stored in `data/settings.json` (only what differs from
`config.json`; “Reset to config.json” deletes it). Order, later wins: defaults, `config.json`, settings page,
environment variables – settings set by an environment variable are shown locked with the variable's name. Address,
port, path, data directory, read-only mode and credentials stay in `config.json`/environment on purpose.

### Multiple networks and VLANs

Magic packets and the ARP scan do not cross routers, so wolweb needs **its own interface in every network** it should
wake and scan – usually an additional VLAN-tagged interface on the machine running wolweb (static address, no
gateway; the switch port or bridge must carry the VLAN tagged). Once it exists, it appears in the settings under
“Interfaces of this machine”; “Add” lists the network, and you can give it a name and VLAN ID.

- **Proxmox LXC:** add a second NIC with a VLAN tag, e.g. `pct set 105 -net1 name=eth1,bridge=vmbr0,tag=20,ip=192.168.20.5/24`
  (the bridge must be VLAN aware).
- **Linux / Docker host:** `ip link add link eth0 name eth0.20 type vlan id 20` (persistently via NetworkManager,
  ifupdown or netplan – examples in the German section [Mehrere Netze und VLANs](#mehrere-netze-und-vlans)); with
  `network_mode: host` the container sees it automatically.
- **Hardening:** bind the web interface to the main network with `host` (e.g. `"host": "192.168.1.5"`), otherwise wolweb
  is also reachable from the VLAN.
- Without an interface, wolweb uses the network's directed broadcast (e.g. `192.168.20.255:9`); it only arrives if the
  router forwards it (OPNsense/pfSense: “UDP Broadcast Relay”), and scanning is not possible there.

### Configuration

`config.json` is optional; the settings page overrides it, environment variables override everything. The most
important settings:

| Key | Variable | Default | Meaning |
|---|---|---|---|
| `port` | `WOLWEB_PORT` | `8089` | port of the web interface |
| `language` | `WOLWEB_LANGUAGE` | `de` | `en` or `de`: language when neither the switch nor the browser name one (also used for notifications) |
| `discovery.networks` | `WOLWEB_NETWORKS` | – | networks, e.g. `192.168.1.0/24` (variable: comma separated), also with name: `{"cidr": "192.168.20.0/24", "name": "IoT", "vlan": 20}` |
| `discovery.interval` | `WOLWEB_SCAN_INTERVAL` | `30m` | automatic scan, `0` = off |
| `status.interval` | `WOLWEB_STATUS_INTERVAL` | `30s` | online check of all devices |
| `bcastip` | `WOLWEBBCASTIP` | automatic | fallback target when neither device nor network provide one |
| `auth.username/password` | `WOLWEB_USER`, `WOLWEB_PASSWORD` | – | login (HTTP Basic – use HTTPS on the internet) |
| `auth.api_token` | `WOLWEB_TOKEN` | – | `Authorization: Bearer …` for automations |
| `auth.public_wake` | `WOLWEB_PUBLIC_WAKE` | `true` | wake links work without login |
| `auth.wake_keys` | `WOLWEB_WAKE_KEYS` | `false` | wake links without login need the device key (`?key=…`) |
| `allowed_hosts` | `WOLWEB_ALLOWED_HOSTS` | – | additional host names, e.g. `wol.example.com` (DNS rebinding protection) |
| `notify.webhook` / `notify.ntfy` | `WOLWEB_WEBHOOK` / `WOLWEB_NTFY` | – | notification targets |

All keys are described in the German [configuration table](#konfiguration) above; their names are the same.

### API

The REST API lives under `/api/v1/` (see the [table above](#api)); changing requests need the header
`X-WolWeb: 1` or a bearer token. Messages follow `Accept-Language` (or `?lang=en`), validation errors name the
affected field (`"field": "mac"`). Wake links stay compatible with the original:
`curl http://host:8089/wake/NAS` → `{"success":true,"message":"Magic packet sent to NAS."}` (with
`Accept-Language: en`).

### Development

`go test ./...` runs all tests; translations live in `internal/i18n/de.json` and `en.json` (a test makes sure both
contain the same keys and every key used by the templates and scripts). Browser tests (Playwright, axe-core) live in
`e2e/`: `cd e2e && npm ci && npx playwright install chromium && npx playwright test` builds wolweb, starts it on
port 8399 with an empty data directory and checks the overview, dialogs, wake and group pages, keyboard use, touch
target sizes and WCAG 2.2 AA. GitHub Actions checks formatting, `go vet`, the race detector, builds for all
platforms and runs the browser tests.
