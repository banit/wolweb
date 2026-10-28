# wolweb

Wake on LAN with a web interface – a Go rewrite of [sameerdhoot/wolweb](https://github.com/sameerdhoot/wolweb):
a single binary with no external dependencies in the browser.

**English** · [Deutsch](README.de.md)

## Quick start

**Requirements:** a machine that is always on and sits in the same network as the devices you want to wake – e.g. a
Raspberry Pi, a NAS or a Proxmox container – running Linux (for Docker: Docker with Compose). Wake on LAN must be
enabled on the target computers (BIOS/UEFI, on Windows also for the network adapter).

1. **Start**

   with Docker:

   ```sh
   git clone https://github.com/banit/wolweb.git && cd wolweb
   docker compose up -d --build
   ```

   or without Docker: download the matching file from [Releases](https://github.com/banit/wolweb/releases)
   (e.g. `wolweb-1.2.0-linux-arm64` for a Raspberry Pi 4/5) and run it:

   ```sh
   chmod +x wolweb-1.2.0-linux-arm64 && sudo ./wolweb-1.2.0-linux-arm64
   ```

2. **Open** `http://<address>:8089` in a browser, e.g. `http://192.168.1.10:8089`.
3. **Add a device:** switch the target computer on, then “Find devices” → “Add”. wolweb detects the home network by
   itself; name, IP and MAC address come from the scan.
4. **Copy the wake link:** the new card offers “Copy wake link” and “Set up on your phone”. From then on, opening
   `http://<address>:8089/wake/<name>` wakes the computer, and the page shows when it answers on the network.
5. **Test it for real:** shut the computer down (leave power and the network cable connected), then open the wake
   link. Only when the page reports the device as reachable does Wake on LAN work – a test with the computer still
   switched on proves nothing.

Everything else – running it permanently with systemd, switching from the original wolweb, updating, multiple
networks and VLANs, configuration, notifications, metrics and the API – follows below.

![Overview with groups, online status and the next scheduled wake-up](docs/screenshots/overview-en.png)

<table>
<tr>
<td width="50%"><img src="docs/screenshots/wake-waiting.png" alt="Wake page: wake-up call sent, waiting for the start"></td>
<td width="50%"><img src="docs/screenshots/wake-online.png" alt="Wake page: device is online"></td>
</tr>
<tr>
<td colspan="2" align="center"><sub>Fixed wake link on a phone: checks, wakes and confirms the start</sub></td>
</tr>
<tr>
<td><img src="docs/screenshots/discovery.png" alt="Network scan with new devices"></td>
<td><img src="docs/screenshots/device-dialog.png" alt="Device dialog in dark mode"></td>
</tr>
<tr>
<td align="center"><sub>Network scan</sub></td>
<td align="center"><sub>Device dialog (dark)</sub></td>
</tr>
</table>

- **Overview** with the online status of every device (ping and TCP ports), search and filters, light/dark mode –
  changes appear instantly in every open window (Server-Sent Events)
- **Fixed wake links** `/wake/<name>`: in a browser a confirmation page that checks the device, sends the magic
  packet and follows the start until the device is really online
- **Reliable waking**: the magic packet goes out three times within one second to ports 9 and 7; if a device with an
  IP address still does not respond after 45 seconds, wolweb sends it once more by itself – for wake links, groups
  and schedules too, when nobody is watching any more
- **Network scan**: finds devices that are switched on in the allowed segments (IP, MAC, host name, vendor) – on
  demand or periodically in the background; add new devices with one click (the name comes from the host name, no
  dialog; only a missing name is asked for) or ignore them
- **Settings page** with an overview of networks and VLANs (which ones wolweb is connected to, devices per network);
  changes apply without a restart
- **Groups** (“Homelab”, “Office”) wake together, also via a fixed group wake link; **schedules** such as
  `Mon-Fri 07:30` wake devices automatically
- **Notifications** via webhook (JSON) or ntfy on your phone when a device goes online/offline or is woken;
  **Prometheus metrics** at `/metrics`
- **History** in words: one sentence per wake attempt (“Wake-up call sent”, “Start confirmed (after 0:42 min)”,
  “Start not confirmed”), who, from where and to where under “Details”; **export/import** of devices as JSON
- **English and German**: the interface follows the browser language and can be switched in the top right corner;
  API messages come in the chosen language too
- Takes over the `devices.json` and the settings of the original wolweb (see
  [Switching from the original wolweb](#switching-from-the-original-wolweb))

## Devices

Only name and MAC address are required. The IP address is needed for the online status (otherwise wolweb takes the
IP from the network scan).

| Field | Purpose |
|---|---|
| Group | Devices with the same group are shown together and can be woken together; existing groups are offered below the field. To assign several devices at once: “Select” in the overview, tick the devices, enter the group name. The group button says how many devices it wakes (“Wake all 4 devices”). Groups themselves – create (also empty), rename, choose members, delete – are managed on the settings page under “Groups” or with the pencil on a group's header |
| Schedule | Switch on “Wake automatically”, tap the days, pick a time – the dialog sums it up (“Monday to Friday at 07:30”) and shows the next wake-up and the time zone. “Pause” suspends the schedule without losing the times. Several times go under “More options” as text, e.g. `Mon-Fri 07:30; Sat,Sun 09:00`, `weekdays 6:45`, `daily 22:00` (German spellings such as `Mo-Fr` work too; `devices.json` always stores the canonical German form, the interface shows it in your language) |
| *Advanced:* Broadcast address | target of the magic packet, only for special cases (otherwise automatic) |
| *Advanced:* Network interface | send the packet through exactly this interface (machines in several networks) |
| *Advanced:* Ports | custom ports for the online check instead of the general list |
| *Advanced:* SecureOn password | 6 bytes, if the network card expects a password in the magic packet |
| *Advanced:* Note | free text on the card |

Schedules run in the local time of the machine or container (`TZ`, preset in `docker-compose.yml`). If it differs
from the browser's time zone, the dialog says so and shows the time in yours. The card shows the next scheduled
wake-up or “Schedule paused”.

## Fixed wake links

| Request | Result |
|---|---|
| `http://host:8089/wake/NAS` in a browser | wake page with progress: *Check device → Send wake-up call → Wait for start → Online* (IP, MAC, target and ping/port/ms under “Technical details”) |
| `curl http://host:8089/wake/NAS` | wakes immediately, JSON answer `{"success":true,"message":…}` (as in the original) |
| `curl "http://host:8089/wake/NAS?wait=120"` | wakes and waits up to 120 s until the device responds (`"online": true/false`; missing if no IP is known) |
| `…/wake/NAS?format=html` / `?format=json` | force the output format |
| `http://host:8089/wake-group/Homelab` | whole group: in a browser a page with the start of every device, otherwise JSON (also `?wait=`) |

The name is not case-sensitive. After renaming, old links keep working: browsers are redirected to the new address,
curl & co. wake directly (wolweb remembers up to ten former names per device; giving one of them to another device
needs a confirmation, and from then on the link belongs to that device – an import picks a free name instead).
Renamed groups keep their old group wake links too, each with the key it was handed out with. The
“online” confirmation needs a known IP (entered or from the network
scan). If the device is already running, the page says so and sends nothing. While waiting, it tells you how long it
keeps checking (`status.wake_timeout`) and whether it will resend automatically. Without an answer, “Start not
confirmed” stays on the page with the obvious checks (power, network cable, a blocked answer), plus “Check again”
(without waking), “Wake again” and technical tips under “Help”. The overview shows the warning on the card until the
device responds. `HEAD` requests (link checkers, previews in messengers) never wake.

Tip: add the wake page to your phone's home screen – then a single tap on the icon wakes the device. Right after
adding a device, its card offers “Copy wake link” and “Set up on your phone” (short instructions for iPhone and
Android). The overview itself can be added as an app the same way.

A device counts as online if it answers ping or one of the ports (`status.ports`) accepts **or refuses** a
connection – a refusal only comes from a running operating system. This applies to devices in wolweb's own network
segment; behind a router only an accepted connection or ping counts, because a firewall there could refuse on the
device's behalf.

The check starts with ping and with the method that worked last time; the remaining ports follow 0.3 s later. A
device that answers ping therefore never gets a single TCP connection (no noise in its SSH log). Custom ports per
device go under “Advanced”, e.g. `32400` for a Plex server.

## Network scan

Usually there is nothing to set up: if the machine is connected to exactly one home network, wolweb scans it by
itself (container bridges, VMs and VPNs do not count). With several, “Find devices” asks once: “Choose the network
your computer is in” – the choice is saved in the settings. Networks can be fixed on the [settings page](#settings)
under “Networks and VLANs” (or in `discovery.networks` / `WOLWEB_NETWORKS`, e.g. `192.168.1.0/24`); such a setting
always takes precedence over the detection. Only networks the machine is directly connected to are scanned – ARP and
Wake on LAN do not cross routers. For more VLANs see [Multiple networks and VLANs](#multiple-networks-and-vlans).

- **Linux with `CAP_NET_RAW`**: active ARP scan – also finds devices that block ping and all ports.
- **Otherwise**: every address gets a UDP packet, then the neighbor table is read (a little less precise).
- Host names from reverse DNS, mDNS (Apple, Linux/Avahi, printers …) and NetBIOS (Windows, Samba), vendors from the
  embedded IEEE list (Wireshark `manuf`).
- No scan finds devices that are switched off – so add devices while they are running.
- Results not seen for 30 days disappear from the list (ignored ones stay).

## Installation

### Docker (recommended)

```sh
docker compose up -d --build
```

`docker-compose.yml` uses `network_mode: host` (required for broadcasts and ARP) and grants the container only
`NET_RAW`. Data is stored in `./data`. Docker Desktop (Windows/macOS) has no real host network – waking and scanning
do not work there.

wolweb runs as root inside the container, but without any extra privileges – so it may only write to folders owned
by root. The easiest way is to let Docker create `./data` on the first start. If you create the folder beforehand,
e.g. to take over an old `devices.json`, give it to root:

```sh
mkdir -p data && cp /path/to/old/devices.json data/legacy-devices.json
sudo chown -R root:root data
```

The `legacy-devices.json` is imported on the first start.

Ready-made images for amd64, arm64 (Raspberry Pi 4/5) and armv7 are built by GitHub for every release tag and
published to `ghcr.io/banit/wolweb` (see `docker-compose.yml`). To build for several platforms yourself:
`docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7 .`

### Directly / Proxmox LXC

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=1.2.0" -o wolweb .
```

Copy it to `/opt/wolweb`, adapt `config.example.json` as `config.json`, copy `deploy/wolweb.service` to
`/etc/systemd/system/` (create the user `wolweb`, make `data/` writable for it), then
`systemctl enable --now wolweb`. The unit grants `CAP_NET_RAW` for ping and the ARP scan and nothing else.

`wolweb -h` lists the command-line options (`-c` configuration file, `-d` devices.json of the original wolweb,
`-healthcheck` for Docker, `-reset-password` see [Password for changes](#password-for-changes)).

## Switching from the original wolweb

wolweb takes over the configuration and device list of the original
[sameerdhoot/wolweb](https://github.com/sameerdhoot/wolweb) without manual work:

- **Without Docker:** put the new binary in place of the old one and start it with the same `config.json` and the
  old device list: `./wolweb -c config.json -d devices.json`. On the first start the devices end up in
  `data/devices.json`; the old `ip` field (e.g. `192.168.1.255:9`) becomes the device's broadcast address. The old
  file stays unchanged.
- **With Docker:** the old image ran under the path `/wolweb` (`WOLWEBVDIR=/wolweb`). To keep bookmarks and
  automations such as `http://host:8089/wolweb/wake/NAS` working, set `WOLWEB_VDIR: "/wolweb"` in the new
  `docker-compose.yml` – otherwise everything is served directly under `/`. Put the old `devices.json` at
  `data/legacy-devices.json` before the first start (see [Docker](#docker-recommended)); your own `config.json` goes to
  `/config/config.json` (prepared in `docker-compose.yml`).
- **Environment variables** of the original (`WOLWEBHOST`, `WOLWEBPORT`, `WOLWEBVDIR`, `WOLWEBBCASTIP`,
  `WOLWEBREADONLY`) keep working.
- **Wake links** `/wake/<name>` still answer curl, scripts and Home Assistant with `{"success":…,"message":…}`; only
  the text of the message is new (so automations should check `"success"`). In a browser they now show the wake page
  with start confirmation. `/data/get` and `/health` still exist.
- **Online status:** the original knows no IP addresses of the devices. Run “Find devices” once while the computers
  are switched on – wolweb assigns the IPs via the MAC addresses and from then on shows whether a device is running.
- **Going back:** the old `devices.json` is left untouched; the old binary or image can be started again at any
  time.

## Updating from 1.0.x / 1.1.x

Devices and settings are taken over automatically on the first start.

1. **Before the update – only if you open wolweb without login via your own domain**, e.g. behind a reverse proxy
   (`https://wol.example.com`): allow that name first. Since 1.1 wolweb only accepts IP addresses and local names
   (`nas`, `*.lan`, `*.local`, `*.fritz.box` …) without login, as protection against DNS rebinding; any other name
   gets a page that explains what to do. Add `"allowed_hosts": ["wol.example.com"]` to `config.json` (older versions
   ignore the key) or `WOLWEB_ALLOWED_HOSTS: "wol.example.com"` to `docker-compose.yml`. If you only find out
   afterwards: open wolweb via its IP address (`http://192.168.1.5:8089`) and enter the name on the settings page
   under “Security” → “Additional host names” (recently rejected names are listed there – only take over one you set
   up yourself). A matching hint is also logged at start-up.
2. **Update:**
   - *Docker, built from the repository:* `git pull && docker compose up -d --build`. If you changed
     `docker-compose.yml` yourself, compare it with the new one (see the *Docker* note under “What changes with 1.2”) instead of overwriting it.
   - *Docker with the ready-made image:* `docker compose pull && docker compose up -d`.
   - *Binary with systemd:* stop wolweb (`systemctl stop wolweb`), replace the file in `/opt/wolweb`, start it again
     (`systemctl start wolweb`). `config.json` and `data/` stay as they are.
3. **Check:** open the overview – the version is shown at the bottom – and look at the log (`docker logs wolweb` or
   `journalctl -u wolweb`) for the backup line and any hints.

**Backup and going back:** before a new version writes the data for the first time, it makes a copy:
`data/backup-<old version>-<date>/` (e.g. `backup-1.0.x-20261028-160512/` with `devices.json`, `discovered.json`
and – from 1.1 on – `state.json` and `settings.json`). This happens once per version change. An older version can
read the new files but drops what it does not know on its next save (SecureOn, wake keys, groups, schedules, custom
ports, history). To go back: stop wolweb, copy the files from the backup folder into `data/`, delete the files from
`data/` that the backup does not contain (for 1.0.x `state.json` and `settings.json`), then start the old version.
Changes made since the update are lost.

**What changes with 1.2 – and whether you need to act:**

- *Network scan without a configured network:* it used to be off. Now wolweb picks the one directly connected home
  network by itself and scans it every 30 minutes (`discovery.interval`). If you do not want that, set
  `discovery.interval` to `0` (settings page or `WOLWEB_SCAN_INTERVAL=0`). With several networks the scan panel
  offers a choice; a configured network still takes precedence.
- *Notifications no longer follow redirects:* if your webhook or ntfy address redirects (e.g. `http://` →
  `https://`), enter the final address – “Send test message” shows whether it works.
- *No double waking:* a second wake-up call for the same device within 10 seconds is not sent but answered with
  `"already": true` – on every path. Automations that deliberately wake twice in a row no longer need to (wolweb
  sends every wake-up call three times and repeats it by itself if needed).
- *New fields in the data:* `schedule_paused` and `aliases` (former names) on devices, plus outcome details in the
  history. An older version drops them on save – pauses, old wake links and the history sentences would be lost;
  the backup (see above) holds the state from before the update.
- *API messages* now say “wake-up call” instead of “magic packet” (e.g. `"message": "Wake-up call sent to NAS."`).
  If an automation checks the exact text, switch it to `"success"`.
- *Default language English:* `language` now defaults to `en`. The interface still follows the browser, but without
  a setting, notifications (webhook, ntfy) and answers to clients that send no language (curl, Home Assistant) are
  now English. To keep German: `"language": "de"` in `config.json`, `WOLWEB_LANGUAGE=de` or “Default language” on
  the settings page. The log is English now, and the backup folder is called `backup-…`. Technical status fields
  change too: `method` says `tcp/445 (refused)` instead of `tcp/445 (abgelehnt)`, and the IP-conflict `note` is
  always English – update automations that compare these strings.
- *Docker:* the new `docker-compose.yml` no longer sets `WOLWEB_NETWORKS` and `WOLWEB_SCAN_INTERVAL` (they are only
  commented examples now) – the network is detected, and the fields on the settings page are no longer locked. If you
  had pinned your network there and want to keep it, copy the line into the new file. Schedules run in the
  container's time zone (`TZ`, preset); if it differs from the browser, the device dialog says so.

## Settings

The gear in the top right corner opens the settings page (`/settings`). There you can change the networks and VLANs,
the network scan, the online check, wait times, notifications (with “Send test message”), public wake links, allowed
host names and the default language. Changes apply immediately, without a restart.

- **Order** (the later one wins): defaults, `config.json`, the settings page, environment variables.
- **Stored** in `data/settings.json` – in the data directory, because it is always writable (`config.json` is often
  mounted read-only in Docker). It only contains what differs from `config.json`; later changes to `config.json`
  therefore still take effect as long as the same value was not also changed on the page. “Reset to config.json”
  deletes the file again.
- **Locked**: whatever is set by an environment variable (in Docker e.g. `WOLWEB_NETWORKS`) is shown greyed out with
  the variable's name. If you prefer to manage it on the page, remove the variable.
- **Not on the page**: address, port, path, data directory, read-only mode and credentials. Otherwise you could lock
  yourself out via the page – or, without login, simply set one.
- Without login and without a [password for changes](#password-for-changes), anyone in the network can view and
  change the settings; the page points this out. In read-only mode it can only be viewed.

![Settings: networks and VLANs with the machine's interfaces](docs/screenshots/settings.png)

### Password for changes

At the bottom of the settings page you can set a password for changes – no username needed. With a password:

- **Viewing and waking stay open** to everyone in the network: overview, status, wake buttons, group wake and the
  fixed wake links.
- **Everything else needs unlocking**: adding, editing and deleting devices, assigning groups, import and export,
  the network scan and the settings page. The lock in the top bar asks for the password; this browser then stays
  unlocked until it is closed or nothing happens for 12 hours. The same button locks again right away.
- Changing or removing the password needs the current one; a new password ends the unlock of every other browser.
  After 5 wrong attempts per address, wolweb makes you wait 30 s (doubling with every further miss, up to 15 min).
- Only a PBKDF2-SHA256 hash is stored, in `data/state.json`; the unlock is a signed cookie (HttpOnly,
  SameSite=Strict).
- Automations with `auth.api_token` are not affected. The HTTP Basic login does not unlock – the whole household
  often knows it, the password for changes is meant for fewer people.
- **Forgotten?** Stop wolweb and start it once with `-reset-password`: it removes the password and exits.

  ```sh
  docker compose stop wolweb
  docker compose run --rm wolweb -reset-password
  docker compose start wolweb
  ```

  Without Docker: stop the service, run `wolweb -c config.json -reset-password` with the same configuration (so it
  finds the data directory) and start the service again.

## Multiple networks and VLANs

Magic packets are broadcasts, and the network scan asks via ARP – neither crosses routers. wolweb therefore needs
**its own interface in every network** in which it should wake and scan: usually an additional VLAN-tagged interface
on the machine running wolweb. Once it exists, it appears on the settings page under “Interfaces of this machine”;
“Add” lists the network, and you can give it a name (“IoT”) and the VLAN ID. wolweb wakes devices in it through the
right interface (the broadcast address follows from the device's IP), and the scan finds them. For devices without an
IP, the device dialog suggests the broadcasts of the configured networks under “Advanced → Broadcast address”.

Requirements: the switch port or bridge of the machine carries the VLAN tagged (trunk), and wolweb has its own fixed
address in the VLAN (outside the DHCP range or via a DHCP reservation). **Do not set a gateway** on the additional
interfaces – the default route stays in the main network.

**Proxmox (LXC):** give the container a second network card with a VLAN tag – in the web interface under
*Container → Network → Add* (bridge `vmbr0`, VLAN tag `20`, IPv4 static, gateway empty) or on the host:

```sh
pct set 105 -net1 name=eth1,bridge=vmbr0,tag=20,ip=10.0.0.5/24
```

The bridge must be “VLAN aware” (*Host → System → Network → vmbr0 → VLAN aware*, or `bridge-vlan-aware yes` in
`/etc/network/interfaces`). For a VM, add another network card with a VLAN tag accordingly.

**Debian, Raspberry Pi OS, Ubuntu (directly or as a Docker host):** create a VLAN interface. To try it out:

```sh
sudo ip link add link eth0 name eth0.20 type vlan id 20
sudo ip addr add 10.0.0.5/24 dev eth0.20
sudo ip link set eth0.20 up
```

Persistently with NetworkManager (Raspberry Pi OS from Bookworm on, Ubuntu Desktop):

```sh
sudo nmcli con add type vlan con-name vlan20 dev eth0 id 20 \
  ipv4.method manual ipv4.addresses 10.0.0.5/24 ipv4.never-default yes ipv6.method disabled
```

with ifupdown (Debian, package `vlan`) in `/etc/network/interfaces.d/vlan20`:

```
auto eth0.20
iface eth0.20 inet static
    address 10.0.0.5/24
```

with netplan (Ubuntu Server) in `/etc/netplan/60-vlan20.yaml`, then `sudo netplan apply`:

```yaml
network:
  version: 2
  vlans:
    vlan20:
      id: 20
      link: eth0
      addresses: [10.0.0.5/24]
```

**Docker:** with `network_mode: host` (as in `docker-compose.yml`) wolweb sees all interfaces of the host – so create
the VLAN interface on the host as above; nothing changes on the container. Without the host network it works with
one macvlan network per VLAN (`docker network create -d macvlan -o parent=eth0.20 --subnet 10.0.0.0/24 vlan20`), but
that is more cumbersome. Docker Desktop (Windows, macOS) can do neither.

**Hardening:** an interface in an isolated network (IoT, guests) also makes wolweb reachable from there. Bind the web
interface to the address in the main network with `host` in `config.json` (or `WOLWEB_HOST`), e.g.
`"host": "192.168.1.5"` – waking and scanning in the VLAN still work.

Without an interface of its own, the settings page shows the network as “not directly connected”, and wolweb wakes
devices in it with a directed broadcast (e.g. `10.0.0.255:9`). It only arrives if the router forwards it into the
VLAN – with OPNsense and pfSense e.g. via the “UDP Broadcast Relay” plugin; most home routers cannot do that, and
scanning is not possible there anyway.

**Single devices behind the router (unicast):** for a single device in another network it also works without an
interface of its own and without broadcast forwarding: wolweb sends the wake-up call straight to the device's IP
address, and the router forwards it like any other packet. For this to work while the device is asleep, the router
needs three things:

1. a **fixed IP address** for the device (DHCP reservation),
2. a **static ARP entry** (IP address → MAC address of the device) in the device's network. A sleeping device does
   not answer ARP requests; without a fixed entry the router drops the wake-up call as soon as it has forgotten the
   MAC address – usually a few minutes after the device went to sleep. With OPNsense under *Interfaces → Neighbors*,
   with pfSense via the static ARP option of the DHCP reservation. Many home routers do not offer this.
3. a **firewall rule** from the wolweb machine to the device for UDP ports 9 and 7 (wolweb sends to both), plus ping
   or one of the checked ports so that wolweb can confirm the start.

In wolweb, enter the device's IP address with port under “Advanced → Broadcast address”, e.g. `10.0.0.20:9`. The
network scan does not find such devices – so enter the MAC address by hand (it is shown in the device's network
settings or in the router's ARP or DHCP overview). Here the device only counts as started once it answers ping or
accepts a connection (see [Fixed wake links](#fixed-wake-links)).

## Configuration

`config.json` (see `config.example.json`) – everything has defaults, the file may be missing. What was changed on the
[settings page](#settings) takes precedence over the file; environment variables override everything. Where two
variable names are given, the first is the one of the original wolweb – both work.

| Key | Variable | Default | Meaning |
|---|---|---|---|
| `host`, `port` | `WOLWEBHOST` / `WOLWEB_HOST`, `WOLWEBPORT` / `WOLWEB_PORT` | `0.0.0.0`, `8089` | address of the web interface |
| `vdir` | `WOLWEBVDIR` / `WOLWEB_VDIR` | `/` | sub path, e.g. `/wolweb` behind a reverse proxy or to keep the original's links |
| `bcastip` | `WOLWEBBCASTIP` / `WOLWEB_BCASTIP` | automatic | default target when neither the device nor a network provide one |
| `read_only` | `WOLWEBREADONLY` / `WOLWEB_READONLY` | `false` | wake only, change nothing |
| `language` | `WOLWEB_LANGUAGE` | `en` | `en` or `de`: language when neither the switch nor the browser name one (also for notifications) |
| `data_dir` | `WOLWEB_DATA_DIR` | `data` | devices (`devices.json`), runtime data (`state.json`), scan results (`discovered.json`) and changes made on the settings page (`settings.json`) |
| `discovery.networks` | `WOLWEB_NETWORKS` | automatic | networks (CIDR, the variable comma separated); empty = the one home network automatically, with several a choice in the interface; also with a name: `{"cidr": "10.0.0.0/24", "name": "IoT", "vlan": 20, "scan": false}` – `scan: false` means show, but do not scan |
| `discovery.interval` | `WOLWEB_SCAN_INTERVAL` | `30m` | automatic scan, `0` = off (at least `1m`) |
| `discovery.max_hosts` | – | `1024` | largest allowed segment |
| `status.interval` | `WOLWEB_STATUS_INTERVAL` | `30s` | online check of all devices, `0` = off (at least `5s`) |
| `status.ports` | – | 22, 80, 443, 445, … | ports for the online check |
| `status.wake_timeout` | `WOLWEB_WAKE_TIMEOUT` | `3m` | how long the wake page waits (`5s` to `10m`) |
| `status.wake_retry` | `WOLWEB_WAKE_RETRY` | `45s` | if a woken machine does not respond for this long, the wake-up call is sent once more; `0` = never |
| `auth.username/password` | `WOLWEB_USER`, `WOLWEB_PASSWORD` | – | login (HTTP Basic) |
| `auth.api_token` | `WOLWEB_TOKEN` | – | `Authorization: Bearer …` for automations |
| `auth.public_wake` | `WOLWEB_PUBLIC_WAKE` | `true` | wake links work without login |
| `auth.wake_keys` | `WOLWEB_WAKE_KEYS` | `false` | wake links without login need the device's key (`?key=…`) |
| `allowed_hosts` | `WOLWEB_ALLOWED_HOSTS` | – | additional host names wolweb may be reached under (see [Security](#security)) |
| `notify.webhook` | `WOLWEB_WEBHOOK` | – | URL that receives every event as JSON via POST |
| `notify.ntfy` | `WOLWEB_NTFY` | – | ntfy topic, e.g. `https://ntfy.sh/my-secret-topic` |
| `notify.events` | `WOLWEB_NOTIFY_EVENTS` | all | selection of `online`, `offline`, `wake`, `wake_failed` |

`devices.json` only contains what you enter yourself; what wolweb observes (last woken, last online, the last 500
wake attempts) is kept in `state.json`. The export at the bottom of the page backs up the devices and their wake-link
keys. For a complete backup or a move to another machine, stop wolweb and copy the whole data directory and your
`config.json` – and keep any environment variables you use for configuration.

A device's broadcast address is determined automatically from the network of its IP; only enter one under “Advanced”
for special cases. If no IP is known, `bcastip` applies; without that setting, the broadcast address of the scanned
segment (if exactly one is configured), otherwise `255.255.255.255:9`. If a network interface is set on the device,
the packet really leaves the machine through that interface on Linux.

A magic packet is a single UDP packet without acknowledgement – if it gets lost (Wi-Fi bridge, a switch port in power
saving mode), nobody notices. wolweb therefore sends it three times, 0.4 seconds apart, to port 9 and port 7, which
network cards listen on depending on the vendor (any other port you enter, e.g. for a port forwarding in the router,
is used alone). If a device with an IP address still does not respond after `status.wake_retry` (45 s), the packet
goes out once more; the history shows this as “automatic retry”. If the device is woken again in the meantime, the
period starts over.

## Notifications and metrics

With `notify.webhook`, a URL receives every event as JSON, e.g. a Home Assistant webhook or Node-RED:

```json
{"event":"online","device":"NAS","id":"3fa2b1c4d5e6","ip":"192.168.1.20","message":"NAS is online (icmp)","time":"…"}
```

With `notify.ntfy`, a push message arrives on your phone (ntfy app, subscribe to the topic). Only real changes
online ↔ offline are reported, not the first result after the start. Notifications use `language`.

wolweb sends notifications exactly to the configured address – servers in your own network included, e.g. a
self-hosted ntfy or Home Assistant. **It does not follow redirects**: if the target answers with a redirect (3xx), the
message is not passed on; instead an error is logged. Otherwise a redirecting server could have the message
delivered to a target nobody configured. Remedy: enter the final address directly (e.g. with `https://` instead of
`http://`). “Send test message” only says “delivered” or “not delivered” – the reason is in the log (`docker logs
wolweb` or `journalctl -u wolweb`). The page thus shows no technical details about internal targets; whether an
address accepted the message is of course still visible to anyone who may use the settings page.

`/metrics` provides metrics in Prometheus format (`wolweb_device_up`, `wolweb_device_rtt_seconds`,
`wolweb_device_last_seen_timestamp_seconds`, `wolweb_device_last_wake_timestamp_seconds`, `wolweb_wakes_total`,
`wolweb_discovery_found` …). With login enabled, scraping needs it too – in Prometheus
`authorization: {credentials: <api_token>}`.

## Security

- **Login**: `auth.username`/`auth.password` (HTTP Basic, on the internet only behind HTTPS) and/or `auth.api_token`
  for automations.
- **Password for changes**: viewing and waking stay open, changes need unlocking – see
  [Password for changes](#password-for-changes).
- **Public wake links** (`auth.public_wake`): with login enabled, `/wake/<name>` and the wake page stay reachable
  without login. So that nobody can wake devices by guessing names, `auth.wake_keys` additionally requires a key per
  device – “Copy wake link” appends it (`/wake/NAS?key=…`), and it can be renewed in the device dialog.
- **No double waking**: every device gets at most one wake-up call every 10 s – with and without login, across wake
  link, group link, wake page and API. A second call within that time (tapped twice, page reloaded) is not an error:
  it reports “already being woken” (`"already": true`), and the page follows the running start without sending
  again. Schedules and the automatic retry are never throttled.
- **Wake links from other websites**: a wake link wakes when it is opened – on purpose, also when it sits on another
  page (Home Assistant dashboard, Glance, a bookmark page). But it also means: any website you visit can send your
  browser to `/wake/<name>` or `/wake-group/<name>`, and the wake page it gets then wakes. Nothing beyond waking is
  possible this way, and `HEAD` (link previews in messengers, link checkers) never wakes. To prevent even that,
  enable the login together with `auth.wake_keys`: then every link – device and group alike – needs the key that
  “Copy wake link” appends, and names cannot be guessed. This is a deliberate exception to the protection against
  cross-site requests: wake links are meant to work from anywhere, every other change needs wolweb's own page or a
  token.
- **DNS rebinding**: without login, wolweb only accepts requests under IP addresses, names without a dot (`nas`),
  local suffixes (`.lan`, `.local`, `.home`, `.internal`, `.home.arpa`, `.fritz.box` …) and the names in
  `allowed_hosts` (`"wol.example.com"`, `"*.example.com"`; `"*"` switches the check off). Otherwise a foreign website
  could point its name at wolweb's IP and remote-control the API in your browser. With login, the check only applies
  when `allowed_hosts` is set.

## Language

The interface is available in English and German. It is chosen in this order: `?lang=en|de` in the address, the
switch in the top right corner (cookie `wolweb_lang`), the browser's language (`Accept-Language`), otherwise
`language` (default `en`). The same applies to the API – automations get German messages with `Accept-Language: de`
or `?lang=de`. Error responses additionally name the affected field (`"field": "mac"`). The texts live in
`internal/i18n/en.json` and `de.json`; a test makes sure both have the same keys and that everything used exists.
The log is English.

## Accessibility

wolweb should also be usable with limited fine motor skills, with the keyboard only or with a screen reader:

- **Simple view** (the button with the large switch in the top right corner): only name, status and one large wake
  button per device, nothing to edit or move. The browser remembers the choice; a link with `?view=simple` switches it
  on (e.g. as a bookmark you set up for someone), `?view=normal` switches it off again.
- **Large targets on touch screens**: on touch devices all buttons, fields and filters are at least 44 × 44 pixels
  and further apart – even an unsteady finger hits them.
- **One tap is enough**: a device's wake link (`/wake/<name>`) wakes it as soon as it is opened. As an icon on the
  phone's home screen that is a single tap, without operating the overview.
- **Keyboard**: everything reachable with Tab, visible focus, a “Skip to devices” link, `/` jumps into the search,
  Escape closes dialogs and the search panel; while the panel is open, the focus stays inside it.
- **Screen readers**: buttons name the device they belong to; the wake and group pages announce the phases (sending,
  waiting, online); filters announce which one is active.
- **Contrast** according to WCAG AA (4.5:1) in light and dark mode; animations are dropped with “reduce motion”.

The browser tests (see [Development](#development)) check this on every push: [axe-core](https://github.com/dequelabs/axe-core)
against WCAG 2.2 AA on all pages and dialogs in light and dark mode, keyboard operation and the size of all touch
targets on a phone.

## API

All changing requests need the header `X-WolWeb: 1` (protection against cross-site requests) or a bearer token;
without it, the answer is 403 with a message for humans and the technical reason in `"error"`. With a
[password for changes](#password-for-changes), changing requests without unlock or token get 403 with
`"locked": true`.

| Method | Path | |
|---|---|---|
| GET | `/api/v1/info` | settings, local networks |
| GET | `/api/v1/events` | live events (Server-Sent Events): `status`, `devices`, `scan` |
| GET / POST | `/api/v1/devices` | list (with status) / add |
| POST | `/api/v1/devices/group` | assign several devices to a group: `{"ids": […], "group": "Homelab"}` (empty = remove from the group) |
| PUT / DELETE | `/api/v1/devices/{id}` | change (sending `updated_at` protects against overwriting) / delete |
| POST | `/api/v1/devices/{id}/wake` | send the magic packet |
| GET | `/api/v1/devices/{id}/status` | check now |
| POST | `/api/v1/devices/{id}/wake-key` | create a new wake key |
| GET | `/api/v1/schedule/preview?s=…` | check a schedule without saving: canonical form, next wake-up, server time zone |
| GET | `/api/v1/groups` | groups in their order, also empty ones, with members (and the key of the group wake link) |
| POST | `/api/v1/groups` | create a group: `{"name": "Office", "members": [ids]}` (members optional) |
| PUT / DELETE | `/api/v1/groups/{name}` | rename and/or set members (`{"name": …, "members": […]}`, without `members` they stay) / delete (the devices stay, without group) |
| POST | `/api/v1/groups/{name}/wake` | wake all devices of a group |
| GET | `/api/v1/history` | wake history, newest first (`?limit=`) |
| GET / POST | `/api/v1/export`, `/api/v1/import` | download devices as JSON / take them over from a file; the answer lists every entry (`items`: `added` – possibly with `new_name` –, `skipped`, `error` with the reason), the interface shows the complete list |
| GET | `/api/v1/discovery` | last scan and results |
| POST | `/api/v1/discovery/scan`, `/api/v1/discovery/cancel` | start / cancel a scan |
| POST | `/api/v1/discovery/{mac}/ignore` | `{"ignored":true}` |
| DELETE | `/api/v1/discovery/{mac}` | remove a scan result |
| POST | `/api/v1/discovery/network` | save the network chosen for the scan |
| GET / PUT / DELETE | `/api/v1/settings` | read / change / reset the settings of the settings page |
| POST | `/api/v1/settings/test-notify` | send a test notification |
| POST | `/api/v1/unlock`, `/api/v1/lock` | unlock this browser with `{"password": "…"}` (sets a cookie) / lock it again |
| PUT | `/api/v1/password` | set, change or remove the password for changes: `{"current": "…", "password": "…"}` (empty `password` removes it) |
| GET | `/wake/{name}`, `/health`, `/data/get` | as in the original |
| GET / POST | `/wake-group/{name}` | group wake link |
| GET | `/metrics` | metrics for Prometheus |

Messages follow the request language (see [Language](#language)); with `Accept-Language: en`, a wake link answers
e.g. `{"success":true,"message":"Wake-up call sent to NAS."}`.

## Development

```sh
go test ./...
go run . -c config.json
```

The interface lives in `internal/web/` (templates and static files, embedded via `go:embed`). To update the vendor
list: `sh tools/gen-oui.sh`. Code comments and commit messages are mostly German; issues and pull requests in
English are welcome.

Browser tests (Playwright with Chromium, needs Go and Node.js) live in `e2e/`. They build wolweb themselves, start it
with an empty data directory on port 8399 (packets only go to 127.0.0.1) and check the overview, device dialog,
search and filters, language, simple view, wake and group pages with all outcomes including the automatic retry, and
accessibility:

```sh
cd e2e
npm ci
npx playwright install chromium
npx playwright test
```

`node screenshots.js` (in the `e2e` folder) regenerates the screenshots in `docs/screenshots/` – with invented
devices, addresses from 192.168.1.x / 10.0.0.x and locally administered MACs, never with real data of the machine.

GitHub Actions checks every push and pull request (gofmt, `go vet`, `go test -race`, builds for Linux
amd64/arm64/armv7, Windows, macOS, browser tests, Docker image for three platforms). A tag `v*`
(`git tag v1.2.0 && git push --tags`) creates a release with ready-made binaries and the image on ghcr.io – afterwards
set the package to public once under “Packages”.

## License

MIT – see [LICENSE](LICENSE). The embedded vendor list comes from Wireshark's
[`manuf`](https://www.wireshark.org/download/automated/data/manuf) (IEEE registry data).
