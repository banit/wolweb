# wolweb

Wake on LAN with a web interface – a Go rewrite of [sameerdhoot/wolweb](https://github.com/sameerdhoot/wolweb):
a single binary with no external dependencies in the browser.

**English** · [Deutsch](#deutsch)

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
   (e.g. `wolweb-1.3.0-linux-arm64` for a Raspberry Pi 4/5) and run it:

   ```sh
   chmod +x wolweb-1.3.0-linux-arm64 && sudo ./wolweb-1.3.0-linux-arm64
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
  changes appear instantly in every open window (Server-Sent Events); groups and devices in your own order (drag and
  drop or arrow buttons)
- **Fixed wake links** `/wake/<name>`: in a browser a confirmation page that checks the device, sends the magic
  packet and follows the start until the device is really online
- **Reliable waking**: the magic packet goes out three times within one second to ports 9 and 7; if a device with an
  IP address still does not respond after 45 seconds, wolweb sends it once more by itself – for wake links, groups
  and schedules too, when nobody is watching any more
- **Network scan**: finds devices that are switched on in the allowed segments (IP, MAC, host name, vendor) – on
  demand or periodically in the background; add new devices with one click (the name comes from the host name, no
  dialog; only a missing name is asked for) or ignore them
- **Settings page** with an overview of networks and VLANs (which ones wolweb is connected to, devices per network)
  and 17 accent colors; changes apply without a restart
- **Password for changes** (optional, no username): everyone in the network can view and wake, editing needs
  unlocking; **privacy**: show the online status – and, separately, when devices were woken – to everyone, only
  after unlocking or to nobody
- **Groups** (“Homelab”, “Office”), managed in the overview or on the settings page, wake together, also via a fixed
  group wake link; **schedules** such as `Mon-Fri 07:30` wake devices automatically
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

**Order:** “Sort” in the overview shows groups and devices as a plain list. Drag them by the handle (mouse, pen or
finger – only the handle, so the page still scrolls on a phone) or use the arrow buttons; at the edge of a group a
device moves on into the next one, also into empty groups and out to “Other devices”. Every change is saved right
away. Until you sort, devices appear by name and groups in the order they were created; new devices go to the end
of their group. The settings page can reorder groups with arrows as well.

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
scan). If the device is already running, the page says so and sends nothing – unless the online status is hidden
([Privacy](#privacy)): then it wakes without this check. While waiting, it tells you how long it
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
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=1.3.0" -o wolweb .
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

## Updating from 1.0.x / 1.1.x / 1.2.x

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
     `docker-compose.yml` yourself, compare it with the new one (see the *Docker* note under “What changes with
     1.2”) instead of overwriting it.
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
Changes made since the update are lost. The wake history, “last woken” and “last online” in the backup's `state.json`
follow the [privacy settings](#privacy) as well: wolweb deletes them there after the retention period, or right away
with recording switched off. Devices, settings and scan results stay in the backup as they were at the update –
delete old backup folders once you no longer need them.

**What changes with 1.3 – and whether you need to act:**

- *Password for changes* (new, optional): nothing changes until you set one on the settings page. With a password,
  viewing and waking stay open; editing, import and export, the network scan and the settings page need unlocking.
  Automations with `auth.api_token` are not affected, the HTTP Basic login does not unlock. See
  [Password for changes](#password-for-changes).
- *Groups are entries of their own:* `devices.json` gets a `groups` list (name, order, former names) and devices an
  `order`. Existing groups are taken over alphabetically, as before; new groups now go to the end, and an empty
  group stays until you delete it (it used to disappear with its last device). `GET /api/v1/groups` lists empty
  groups too, in their order. An older version drops `groups` and `order` on save – the backup (see above) holds
  the state from before the update.
- *Accent color* (new): default `indigo` as before.
- *Privacy* (new): `privacy.online_status` can hide the online status, `privacy.wake_history` when devices were
  woken; both default to `all` as before.
- *History expires* (new): the wake history, “last woken” and “last online” are now deleted after 30 days
  (`privacy.retention_days`, up to 365); `privacy.recording: "off"` stores none of them. On the first start, older
  history entries disappear – the backup (see above) still has them.
- *Behind a reverse proxy:* without login, wolweb now also checks the host name the proxy passes on
  (`X-Forwarded-Host`, `Forwarded`). If that is your own domain, allow it as described in step 1.
- *Check ports:* `status.ports` takes at most 16 ports (duplicates are dropped); with more, wolweb does not start.

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
host names, who sees the online status and when devices were woken ([Privacy](#privacy)), the default language and
the accent color (17 colors,
each in a light and a dark variant). Changes apply immediately, without a restart. Groups and the password for
changes have sections of their own there that save right away.

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
  the network scan (“Find devices” and the scan results only appear after unlocking) and the settings page. The
  lock in the top bar asks for the password; this browser then stays unlocked until it is closed or nothing is
  edited for 12 hours (just leaving the overview open does not count). The same button locks again right away.
- Changing or removing the password needs the current one; a new password ends the unlock of every other browser.
  After 5 wrong attempts from one browser, wolweb makes it wait 30 s (doubling with every further miss, up to
  15 min); behind a reverse proxy the last `X-Forwarded-For` entry tells browsers apart. After 30 wrong attempts in
  total the wait applies to everyone, and only one password check runs at a time.
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

### Privacy

Under “Privacy” on the settings page you choose who sees whether devices are running (`privacy.online_status`):

| Choice | Value | Who sees the online status |
|---|---|---|
| Show to everyone (default) | `all` | everyone who opens wolweb |
| Show only after unlocking | `unlocked` | only browsers unlocked with the [password for changes](#password-for-changes) and automations with `auth.api_token` – for example to troubleshoot. Without a password for changes nobody sees it. |
| Show to nobody | `none` | nobody, not even after unlocking |

Hidden means hidden everywhere, not just in the overview: cards show no status, no green dot and no “Wake again”,
the online/offline filter disappears and groups only count their devices. The server leaves the status out of
`/api/v1/devices` and `/api/v1/groups`, live events, `/metrics` and the history; `…/status` endpoints answer 403
with `"hidden": true`. **Waking is the exception:** whoever wakes a device – with the button in the overview or
via its wake or group link – starts it anyway, so they still see whether it comes up. The overview card follows the
start of that one device and then hides the status again; wake and group pages confirm the start as usual. The
wake answer carries a short-lived `watch` key that `…/status?watch=…` accepts for that device or group for 15
minutes, and `?wait=` keeps working. Only the check beforehand whether the device is already running is skipped –
it would reveal the status without waking. Scan results reveal who is on the network too; with a password for
changes only unlocked browsers see them, whatever the setting. wolweb still checks the devices in the background,
for the automatic retry and notifications (choose the notification events separately). The HTTP Basic login does
not count as unlocked, as for the password for changes.

**“Last woken” and history** (`privacy.wake_history`): a choice of its own below the online status, with the same
three levels and `all` as default – so nothing changes until you pick another one. It hides when devices were woken:
“woken … ago” on the cards, the “History” button (`/api/v1/history` answers 403 with `"hidden": true`), `last_wake`
in the API and the metrics `wolweb_device_last_wake_timestamp_seconds` and `wolweb_wakes_total`. With `unlocked`,
unlocked browsers and the API token still see all of it. Waking is not affected: whoever wakes a device follows
their own wake-up call as usual, and the history is still recorded – switch back and it shows up again.

**Recording and retention** (`privacy.recording`, `privacy.retention_days`): the wake history, “last woken” and
“last online” are personal data – who woke a device when, and when a workstation was running. wolweb deletes them
after the set number of days (1 to 365, default 30): the pages leave older entries out right away, and `state.json`
is cleaned up every five minutes. Unticking “Record the wake history and “last online”” (`recording: "off"`) deletes
them at once, after a confirmation, and stores none of them from then on; the “History” button disappears. Waking and
the start confirmation keep working either way. The server log (`docker logs wolweb`, `journalctl -u wolweb`) is not
affected – Docker or journald keep it; limit it there if needed.

## Multiple networks and VLANs

Magic packets are broadcasts, and the network scan asks via ARP – neither crosses routers. wolweb therefore needs
**its own interface in every network** in which it should wake and scan: usually an additional VLAN-tagged interface
on the machine running wolweb. Once it exists, it appears on the settings page under “Interfaces of this machine”;
“Add” lists the network, and you can give it a name (“IoT”) and the VLAN ID. wolweb wakes devices in it through the
right interface (the broadcast address follows from the device's IP), and the scan finds them. For devices without an
IP, the device dialog suggests the broadcasts of the configured networks under “Advanced → Broadcast address”.
With several networks, give each device an IP or a broadcast address: without both, its wake target follows the
network the scan last saw it in (a change of network only counts after a week without a sighting in the old one).

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
| `accent` | `WOLWEB_ACCENT` | `indigo` | accent color of the interface (buttons, selection, highlights) on every page: indigo, blue, sky, cyan, teal, emerald, green, lime, amber, orange, red, rose, pink, fuchsia, purple, violet, slate – easiest picked on the settings page |
| `data_dir` | `WOLWEB_DATA_DIR` | `data` | devices (`devices.json`), runtime data (`state.json`), scan results (`discovered.json`) and changes made on the settings page (`settings.json`) |
| `discovery.networks` | `WOLWEB_NETWORKS` | automatic | networks (CIDR, the variable comma separated); empty = the one home network automatically, with several a choice in the interface; also with a name: `{"cidr": "10.0.0.0/24", "name": "IoT", "vlan": 20, "scan": false}` – `scan: false` means show, but do not scan |
| `discovery.interval` | `WOLWEB_SCAN_INTERVAL` | `30m` | automatic scan, `0` = off (at least `1m`) |
| `discovery.max_hosts` | – | `1024` | largest allowed segment |
| `status.interval` | `WOLWEB_STATUS_INTERVAL` | `30s` | online check of all devices, `0` = off (at least `5s`) |
| `status.ports` | – | 22, 80, 443, 445, … | ports for the online check (at most 16) |
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
| `privacy.online_status` | `WOLWEB_ONLINE_STATUS` | `all` | who sees whether devices are running: `all`, `unlocked` (only after unlocking) or `none` (see [Privacy](#privacy)) |
| `privacy.wake_history` | `WOLWEB_WAKE_HISTORY` | `all` | who sees when devices were woken (“woken … ago”, history): `all`, `unlocked` or `none` |
| `privacy.recording` | `WOLWEB_RECORDING` | `on` | `off` = store no wake history, “last woken” or “last online” (and delete what is there) |
| `privacy.retention_days` | `WOLWEB_RETENTION_DAYS` | `30` | after how many days the wake history, “last woken” and “last online” are deleted (1–365) |

`devices.json` only contains what you enter yourself; what wolweb observes (last woken, last online, up to 500 wake
attempts of the last `privacy.retention_days` days) is kept in `state.json`. The export at the bottom of the page backs up the devices and their wake-link
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
`authorization: {credentials: <api_token>}`. If the online status is hidden ([Privacy](#privacy)), `wolweb_device_up`,
`wolweb_device_rtt_seconds` and `wolweb_device_last_seen_timestamp_seconds` stay empty – with `unlocked`, the token
brings them back. `privacy.wake_history` does the same for `wolweb_device_last_wake_timestamp_seconds` and
`wolweb_wakes_total`. Automatic retries count in neither of them.

## Security

- **Login**: `auth.username`/`auth.password` (HTTP Basic, on the internet only behind HTTPS) and/or `auth.api_token`
  for automations.
- **Login**: credentials (HTTP Basic or API token) are checked one at a time, and a failed login holds the check for
  0.5 s – at most two per second in total; failed logins are noted in the log (at most once a minute). Browsers and
  clients that logged in within the last 7 days are checked right away, so an attack does not lock them out; a first
  login may have to wait during an attack (up to about 16 s, or “try again shortly”), public wake pages stay
  reachable. Still use a long password and a long random token; wolweb warns at startup if they are short.
- **Password for changes**: viewing and waking stay open, changes need unlocking – see
  [Password for changes](#password-for-changes).
- **Online status and wake history**: who sees whether devices are running and when they were woken – everyone,
  only after unlocking or nobody – see [Privacy](#privacy).
- **Public wake links** (`auth.public_wake`): with login enabled, `/wake/<name>` and the wake page stay reachable
  without login. So that nobody can wake devices by guessing names, `auth.wake_keys` additionally requires a key per
  device – “Copy wake link” appends it (`/wake/NAS?key=…`), and it can be renewed in the device dialog (group keys in the group dialog). A group that is deleted and created again under the same name gets a new key.
- **No double waking**: every device gets at most one wake-up call every 10 s – with and without login, across wake
  link, group link, wake page and API. A second call within that time (tapped twice, page reloaded) is not an error:
  it reports “already being woken” (`"already": true`), and the page follows the running start without sending
  again. If sending fails, further calls within 3 s get the same error without sending again. Schedules and the
  automatic retry are never throttled.
- **Wake links from other websites**: a wake link wakes when it is opened – on purpose, also when it sits on another
  page (Home Assistant dashboard, Glance, a bookmark page). But it also means: any website you visit can send your
  browser to `/wake/<name>` or `/wake-group/<name>`, and the wake page it gets then wakes. Nothing beyond waking is
  possible this way, and `HEAD` (link previews in messengers, link checkers) never wakes. To prevent even that,
  enable the login together with `auth.wake_keys`: then every link – device and group alike – needs the key that
  “Copy wake link” appends, and names cannot be guessed. A browser logged in to wolweb needs no key; if another
  website sends it to a wake link without the key, the wake page only wakes after a click. This is a deliberate exception to the protection against
  cross-site requests: wake links are meant to work from anywhere, every other change needs wolweb's own page or a
  token.
- **DNS rebinding**: without login, wolweb only accepts requests under IP addresses, names without a dot (`nas`),
  local suffixes (`.lan`, `.local`, `.home`, `.internal`, `.home.arpa`, `.fritz.box` …) and the names in
  `allowed_hosts` (`"wol.example.com"`, `"*.example.com"`; `"*"` switches the check off). Otherwise a foreign website
  could point its name at wolweb's IP and remote-control the API in your browser. With login, the check only applies
  when `allowed_hosts` is set. Behind a reverse proxy, let it pass the original host name on (nginx:
  `proxy_set_header Host $host;`, Apache: `ProxyPreserveHost On`); names a proxy reports in `X-Forwarded-Host` or
  `Forwarded: host=` must be allowed as well.

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
- **Sorting without dragging**: under “Sort” every group and device also has arrow buttons; the focus stays on the
  moved entry, and screen readers hear its new position.
- **Screen readers**: buttons name the device they belong to; the wake and group pages announce the phases (sending,
  waiting, online); filters announce which one is active.
- **Contrast** according to WCAG AA (4.5:1) in light and dark mode, with every accent color; animations are dropped
  with “reduce motion”.

The browser tests (see [Development](#development)) check this on every push: [axe-core](https://github.com/dequelabs/axe-core)
against WCAG 2.2 AA on all pages and dialogs in light and dark mode, keyboard operation and the size of all touch
targets on a phone.

## API

All changing requests need the header `X-WolWeb: 1` (protection against cross-site requests) or a bearer token;
without it, the answer is 403 with a message for humans and the technical reason in `"error"`. With a
[password for changes](#password-for-changes), changing requests without unlock or token get 403 with
`"locked": true`. If the online status is hidden ([Privacy](#privacy)), lists and events come without status,
`…/status` answers 403 with `"hidden": true`, and the answer to a wake request carries the `watch` key for following
the start. With `privacy.wake_history`, `last_wake` is missing and `/api/v1/history` answers 403 with
`"hidden": true` as well.

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
| PUT | `/api/v1/order` | order as in the overview: `{"groups": [names], "devices": [{"group": "Office", "ids": […]}, {"group": "", "ids": […]}]}` – a device listed under another group moves there |
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
| GET | `/wake/{name}/status`, `/wake-group/{name}/status` | status for the wake and group pages (`?watch=…` with the online status hidden) |
| GET | `/metrics` | metrics for Prometheus |

Messages follow the request language (see [Language](#language)); with `Accept-Language: en`, a wake link answers
e.g. `{"success":true,"message":"Wake-up call sent to NAS."}`.

## Development

```sh
go test ./...
go run . -c config.json
```

The interface lives in `internal/web/` (templates and static files, embedded via `go:embed`). To update the vendor
list: `sh tools/gen-oui.sh`. Code comments are mostly German, older commit messages too; issues and pull requests in
English are welcome.

Browser tests (Playwright with Chromium, needs Go and Node.js) live in `e2e/`. They build wolweb themselves, start it
with an empty data directory on port 8399 and check the overview, device dialog,
search and filters, language, simple view, sorting, groups, the password for changes, privacy, the settings page,
wake and group pages with all outcomes including the automatic retry, and accessibility. The test server stays off
the real network: wake-up calls go to 127.0.0.1, the automatic network scan is off and the invented scan results live
on 127.0.0.x. Only the “offline” test device is checked in the documentation network 198.51.100.0/24 – no real device
uses it, and an address on the machine itself would always answer:

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
(`git tag v1.3.0 && git push --tags`) creates a release with ready-made binaries and the image on ghcr.io – afterwards
set the package to public once under “Packages”.

## License

MIT – see [LICENSE](LICENSE). The embedded vendor list comes from Wireshark's
[`manuf`](https://www.wireshark.org/download/automated/data/manuf) (IEEE registry data).

<!-- German documentation: the same text as README.de.md (a test in internal/config checks it) -->

# Deutsch

Wake on LAN mit Weboberfläche – Neufassung von [sameerdhoot/wolweb](https://github.com/sameerdhoot/wolweb) in Go,
als einzelne Datei ohne externe Abhängigkeiten im Browser.

[English](#wolweb) · **Deutsch**

## Schnellstart

**Voraussetzungen:** ein Rechner, der ständig läuft und im selben Netz hängt wie die Geräte, die geweckt werden
sollen – z. B. ein Raspberry Pi, ein NAS oder ein Proxmox-Container – mit Linux (für Docker: Docker mit Compose).
Auf den Zielrechnern muss Wake on LAN eingeschaltet sein (BIOS/UEFI, unter Windows zusätzlich bei der
Netzwerkkarte).

1. **Starten**

   mit Docker:

   ```sh
   git clone https://github.com/banit/wolweb.git && cd wolweb
   docker compose up -d --build
   ```

   oder ohne Docker: die passende Datei unter [Releases](https://github.com/banit/wolweb/releases) laden
   (z. B. `wolweb-1.3.0-linux-arm64` für einen Raspberry Pi 4/5) und starten:

   ```sh
   chmod +x wolweb-1.3.0-linux-arm64 && sudo ./wolweb-1.3.0-linux-arm64
   ```

2. **Öffnen:** `http://<Adresse>:8089` im Browser, z. B. `http://192.168.1.10:8089`.
3. **Gerät übernehmen:** den Zielrechner einschalten, „Geräte suchen“ → „Übernehmen“. Das Heimnetz erkennt wolweb
   selbst; Name, IP- und MAC-Adresse kommen aus der Suche.
4. **Wecklink kopieren:** Die neue Karte bietet „Wecklink kopieren“ und „Auf dem Handy einrichten“. Ab dann weckt
   das Öffnen von `http://<Adresse>:8089/wake/<Name>` den Rechner, und die Seite zeigt, wann er im Netzwerk antwortet.
5. **Richtig testen:** den Rechner herunterfahren (Strom und Netzwerkkabel bleiben dran), dann den Wecklink öffnen.
   Erst wenn die Seite das Gerät als erreichbar meldet, funktioniert Wake on LAN – ein Test am noch eingeschalteten
   Rechner beweist nichts.

Alles Weitere – Dauerbetrieb mit systemd, Update, mehrere Netze und VLANs, Konfiguration, Benachrichtigungen,
Metriken und die API – steht weiter unten.

![Übersicht mit Gruppen, Online-Status und nächster geplanter Weckzeit](docs/screenshots/overview-light.png)

<table>
<tr>
<td width="50%"><img src="docs/screenshots/wake-waiting.png" alt="Weckseite: Weckruf gesendet, wartet auf den Start"></td>
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
  erscheinen sofort in jedem offenen Fenster (Server-Sent Events); Gruppen und Geräte in eigener Reihenfolge (per
  Ziehen oder mit Pfeilknöpfen)
- **Feste Wecklinks** `/wake/<Name>`: im Browser eine Bestätigungsseite, die das Gerät prüft, das Magic Packet
  sendet und den Start verfolgt, bis das Gerät wirklich online ist
- **Zuverlässiges Wecken**: Das Magic Packet geht dreimal innerhalb einer Sekunde an Port 9 und 7; antwortet ein
  Gerät mit IP nach 45 Sekunden noch nicht, schickt wolweb es von selbst noch einmal – auch bei Wecklinks,
  Gruppen und Zeitplänen, wenn niemand mehr zuschaut
- **Netzwerksuche**: findet eingeschaltete Geräte in freigegebenen Segmenten (IP, MAC, Hostname, Hersteller) –
  per Hand oder regelmäßig im Hintergrund; neue Geräte mit einem Klick übernehmen (Name aus dem Hostnamen, ohne Dialog; fehlt er, wird nur
  der Name gefragt) oder ignorieren
- **Einstellungsseite** mit Übersicht der Netze und VLANs (an welchen wolweb hängt, Geräte je Netz) und 17
  Akzentfarben; Änderungen gelten ohne Neustart
- **Passwort für Änderungen** (optional, ohne Benutzername): Ansehen und Wecken kann jeder im Netz, Ändern erst nach
  dem Entsperren; **Privatsphäre**: Online-Status – und getrennt davon, wann geweckt wurde – für alle, erst nach dem
  Entsperren oder für niemanden
- **Gruppen** („Homelab“, „Büro“), verwaltet in der Übersicht oder auf der Einstellungsseite, gemeinsam wecken, auch
  per festem Gruppen-Wecklink; **Zeitpläne** wie `Mo-Fr 07:30` wecken Geräte automatisch
- **Benachrichtigungen** per Webhook (JSON) oder ntfy aufs Handy, wenn ein Gerät online/offline geht oder
  geweckt wird; **Prometheus-Metriken** unter `/metrics`
- **Verlauf** in Worten: je Weckversuch ein Satz („Weckruf gesendet“, „Start bestätigt (nach 0:42 min)“, „Start nicht
  bestätigt“), wer, woher und wohin unter „Details“; **Export/Import** der Geräte als JSON
- **Deutsch und Englisch**: Die Oberfläche folgt der Sprache des Browsers und lässt sich oben rechts umschalten;
  auch die Meldungen der API kommen in der gewählten Sprache
- Übernimmt die `devices.json` und die Einstellungen des ursprünglichen wolweb (siehe
  [Umstieg](#umstieg-vom-ursprünglichen-wolweb))

## Geräte

Pflicht sind nur Name und MAC-Adresse. Die IP-Adresse braucht es für den Online-Status (sonst nimmt wolweb die IP
aus der Netzwerksuche).

| Feld | Wofür |
|---|---|
| Gruppe | Geräte mit gleicher Gruppe stehen zusammen und lassen sich gemeinsam wecken; vorhandene Gruppen stehen unter dem Feld zum Antippen. Mehrere Geräte auf einmal zuordnen: in der Übersicht „Auswählen“, Geräte ankreuzen, Gruppenname eingeben. Der Gruppenknopf nennt, wie viele er weckt („Alle 4 Geräte wecken“). Die Gruppen selbst – anlegen (auch leer), umbenennen, Mitglieder wählen, löschen – verwaltest du auf der Einstellungsseite unter „Gruppen“ oder mit dem Stift am Gruppenkopf |
| Zeitplan | „Automatisch wecken“ einschalten, Wochentage antippen, Uhrzeit wählen – der Dialog zeigt dazu „Montag bis Freitag um 07:30 Uhr“, das nächste Wecken und die Zeitzone. „Pausieren“ setzt den Zeitplan aus, ohne die Zeiten zu vergessen. Mehrere Zeiten gehen unter „Weitere Möglichkeiten“ als Text, z. B. `Mo-Fr 07:30; Sa,So 09:00`, `werktags 6:45`, `täglich 22:00` (englisch wie `Mon-Fri 07:30` geht auch; gespeichert wird einheitlich die deutsche Form) |
| *Erweitert:* Broadcast-Adresse | Ziel des Magic Packets, nur für Sonderfälle (sonst automatisch) |
| *Erweitert:* Netzwerkschnittstelle | Paket über genau diese Schnittstelle senden (Rechner mit mehreren Netzen) |
| *Erweitert:* Ports | eigene Ports für die Online-Prüfung statt der allgemeinen Liste |
| *Erweitert:* SecureOn-Passwort | 6 Byte, falls die Netzwerkkarte ein Passwort im Magic Packet verlangt |
| *Erweitert:* Notiz | freier Text auf der Karte |

Zeitpläne gelten in der Ortszeit des Rechners bzw. Containers (`TZ`, in `docker-compose.yml` voreingestellt).
Weicht sie von der Zeitzone des Browsers ab, sagt der Dialog das und nennt die Uhrzeit bei dir. Die Karte zeigt die
nächste geplante Weckzeit oder „Zeitplan pausiert“.

**Reihenfolge:** „Sortieren“ in der Übersicht zeigt Gruppen und Geräte als schlichte Liste. Ziehen geht am Griff (Maus,
Stift oder Finger – nur am Griff, damit die Seite auf dem Handy weiter blättert), alternativ mit den Pfeilknöpfen;
am Rand einer Gruppe wandert ein Gerät in die nächste, auch in leere Gruppen und zu „Weitere Geräte“. Jede Änderung
wird sofort gespeichert. Solange nichts sortiert ist, stehen Geräte nach Namen und Gruppen in der Reihenfolge, in
der sie angelegt wurden; neue Geräte kommen ans Ende ihrer Gruppe. Gruppen lassen sich auch auf der
Einstellungsseite mit Pfeilen umstellen.

## Feste Wecklinks

| Aufruf | Ergebnis |
|---|---|
| `http://host:8089/wake/NAS` im Browser | Weckseite mit Fortschritt: *Gerät prüfen → Weckruf senden → Auf Start warten → Online* (IP, MAC, Ziel und Ping/Port/ms unter „Technische Details“) |
| `curl http://host:8089/wake/NAS` | weckt sofort, Antwort JSON `{"success":true,"message":…}` (wie im Original) |
| `curl "http://host:8089/wake/NAS?wait=120"` | weckt und wartet bis zu 120 s, bis das Gerät antwortet (`"online": true/false`; fehlt, wenn keine IP bekannt ist) |
| `…/wake/NAS?format=html` / `?format=json` | Ausgabe erzwingen |
| `http://host:8089/wake-group/Homelab` | ganze Gruppe: im Browser eine Seite mit dem Start jedes Geräts, sonst JSON (auch `?wait=`) |

Der Name ist nicht von Groß-/Kleinschreibung abhängig. Nach dem Umbenennen gelten alte Links weiter: Browser
werden zur neuen Adresse geleitet, curl & Co. wecken direkt (wolweb merkt sich je Gerät bis zu zehn frühere Namen;
einen davon einem anderen Gerät zu geben, muss man bestätigen, danach gehört der Link dem neuen Gerät – ein Import
wählt stattdessen einen freien Namen). Auch umbenannte Gruppen behalten ihre alten Gruppen-Wecklinks, jeweils mit
dem Schlüssel, mit dem sie herausgegeben wurden. Die Bestätigung „online“ gibt es nur, wenn eine IP bekannt
ist (eingetragen oder aus der Netzwerksuche). Läuft das Gerät schon, sagt die Seite das und sendet nichts – außer
der Online-Status ist ausgeblendet ([Privatsphäre](#privatsphäre)): Dann weckt sie ohne diese Prüfung. Während
des Wartens nennt sie, wie lange geprüft wird (`status.wake_timeout`) und ob automatisch erneut gesendet wird. Kommt
keine Antwort, bleibt „Start nicht bestätigt“ mit dem Naheliegenden stehen (Strom, Netzwerkkabel, blockierte
Rückmeldung), dazu „Nochmals prüfen“ (ohne zu wecken), „Erneut wecken“ und technische Tipps unter „Hilfe“. Die
Übersicht zeigt die Warnung auf der Karte, bis das Gerät antwortet.
`HEAD`-Anfragen (Linkprüfer, Vorschauen in Messengern) wecken nie.

Tipp: Die Weckseite auf dem Handy „Zum Home-Bildschirm“ hinzufügen – dann weckt ein Tipp aufs Symbol das Gerät.
Nach dem Anlegen bietet die Karte dafür „Wecklink kopieren“ und „Auf dem Handy einrichten“ (kurze Anleitung für
iPhone und Android). Die Übersicht selbst lässt sich ebenso als App ablegen.

Online gilt ein Gerät, wenn es auf Ping antwortet oder einer der Ports (`status.ports`) eine Verbindung annimmt **oder
ablehnt** – eine Ablehnung kommt nur von einem laufenden Betriebssystem. Das gilt für Geräte im eigenen Netzsegment;
hinter einem Router zählt nur eine angenommene Verbindung oder Ping, weil dort auch eine Firewall stellvertretend
ablehnen kann.

Geprüft wird zuerst per Ping und mit der Methode, die beim letzten Mal geklappt hat; die übrigen Ports kommen erst
0,3 s später dran. Ein Gerät, das auf Ping antwortet, bekommt so keine einzige TCP-Verbindung (kein Rauschen im
SSH-Log). Pro Gerät lassen sich unter „Erweitert“ eigene Ports eintragen, z. B. `32400` für einen Plex-Server.

## Netzwerksuche

Einrichten muss man dafür meist nichts: Hängt der Rechner an genau einem Heimnetz, sucht wolweb dort von selbst
(Container-Brücken, VMs und VPNs zählen nicht). Hat er mehrere, fragt „Geräte suchen“ einmal: „Wähle das Netzwerk, in
dem dein Rechner steht“ – die Wahl landet in den Einstellungen. Fest vorgeben lassen sich die Netze in den
[Einstellungen](#einstellungen) unter „Netze und VLANs“ (oder in `discovery.networks` bzw. `WOLWEB_NETWORKS`, etwa
`192.168.1.0/24`); eine solche Angabe geht der Erkennung immer vor. Gesucht wird nur in Netzen, an denen der Rechner
direkt hängt – ARP und Wake on LAN gehen nicht über Router. Für weitere VLANs siehe
[Mehrere Netze und VLANs](#mehrere-netze-und-vlans).

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
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=1.3.0" -o wolweb .
```

Nach `/opt/wolweb` kopieren, `config.example.json` als `config.json` anpassen, `deploy/wolweb.service` nach
`/etc/systemd/system/` (Benutzer `wolweb` anlegen, `data/` beschreibbar machen), dann
`systemctl enable --now wolweb`. Die Unit gewährt nur `CAP_NET_RAW` für Ping und ARP-Scan, sonst nichts.

`wolweb -h` zeigt die Kommandozeilenoptionen (`-c` Konfigurationsdatei, `-d` devices.json des ursprünglichen wolweb,
`-healthcheck` für Docker, `-reset-password` siehe [Passwort für Änderungen](#passwort-für-änderungen)).

## Umstieg vom ursprünglichen wolweb

wolweb übernimmt Konfiguration und Geräteliste des ursprünglichen
[sameerdhoot/wolweb](https://github.com/sameerdhoot/wolweb) ohne Handarbeit:

- **Ohne Docker:** das neue Programm an die Stelle des alten setzen und mit derselben `config.json` und der alten
  Geräteliste starten: `./wolweb -c config.json -d devices.json`. Beim ersten Start landen die Geräte in
  `data/devices.json`; das alte Feld `ip` (z. B. `192.168.1.255:9`) wird zur Broadcast-Adresse des Geräts. Die alte
  Datei bleibt unverändert.
- **Mit Docker:** Das alte Image lief unter dem Pfad `/wolweb` (`WOLWEBVDIR=/wolweb`). Damit Lesezeichen und
  Automationen wie `http://host:8089/wolweb/wake/NAS` weiter funktionieren, in der neuen `docker-compose.yml`
  `WOLWEB_VDIR: "/wolweb"` setzen – sonst liegt alles direkt unter `/`. Die alte `devices.json` vor dem ersten Start
  als `data/legacy-devices.json` ablegen (siehe [Docker](#docker-empfohlen)); eine eigene `config.json` gehört nach
  `/config/config.json` (in `docker-compose.yml` vorbereitet).
- **Umgebungsvariablen** des Originals (`WOLWEBHOST`, `WOLWEBPORT`, `WOLWEBVDIR`, `WOLWEBBCASTIP`, `WOLWEBREADONLY`)
  gelten weiter.
- **Wecklinks** `/wake/<Name>` antworten curl, Skripten und Home Assistant wie bisher mit
  `{"success":…,"message":…}`; nur der Text der Meldung ist neu (Automationen also auf `"success"` prüfen). Im Browser
  kommt jetzt die Weckseite mit Startbestätigung. `/data/get` und `/health` gibt es weiterhin.
- **Online-Status:** Das Original kennt keine IP-Adressen der Geräte. Einmal „Geräte suchen“, während die Rechner
  eingeschaltet sind – wolweb ordnet die IP über die MAC-Adresse zu und zeigt ab dann, ob ein Gerät läuft.
- **Zurück:** Die alte `devices.json` bleibt unverändert; das alte Programm bzw. Image lässt sich jederzeit wieder
  starten.

## Update von 1.0.x / 1.1.x / 1.2.x

Geräte und Einstellungen werden beim ersten Start automatisch übernommen.

1. **Vor dem Update – nur wenn du wolweb ohne Anmeldung über eine eigene Domain aufrufst**, etwa hinter einem
   Reverse-Proxy (`https://wol.example.com`): diesen Namen zuerst freigeben. Seit 1.1 nimmt wolweb ohne Anmeldung nur
   IP-Adressen und lokale Namen (`nas`, `*.lan`, `*.local`, `*.fritz.box` …) an, als Schutz vor DNS-Rebinding; unter
   anderen Namen erscheint eine Seite, die erklärt, was zu tun ist. In `config.json` `"allowed_hosts":
   ["wol.example.com"]` eintragen (ältere Versionen übergehen den Schlüssel) bzw. in `docker-compose.yml`
   `WOLWEB_ALLOWED_HOSTS: "wol.example.com"`. Merkst du es erst danach: wolweb über seine IP-Adresse öffnen
   (`http://192.168.1.5:8089`) und den Namen in den Einstellungen unter „Sicherheit“ → „Weitere Hostnamen“ eintragen
   (zuletzt abgewiesene Namen stehen dort zur Auswahl – nur einen übernehmen, den du selbst eingerichtet hast). Beim
   Start steht außerdem ein Hinweis im Log.
2. **Aktualisieren:**
   - *Docker, aus dem Repository gebaut:* `git pull && docker compose up -d --build`. Wer `docker-compose.yml` selbst
     angepasst hat, vergleicht sie mit der neuen (siehe den Punkt *Docker* unter „Was sich mit 1.2 ändert“), statt sie
     zu überschreiben.
   - *Docker mit fertigem Image:* `docker compose pull && docker compose up -d`.
   - *Programmdatei mit systemd:* wolweb stoppen (`systemctl stop wolweb`), die Datei in `/opt/wolweb` ersetzen, wieder
     starten (`systemctl start wolweb`). `config.json` und `data/` bleiben, wie sie sind.
3. **Prüfen:** die Übersicht öffnen – unten steht die Version – und im Log (`docker logs wolweb` bzw.
   `journalctl -u wolweb`) nach der Zeile zur Sicherung und nach Hinweisen sehen.

**Sicherung und Weg zurück:** Bevor eine neue Version die Daten zum ersten Mal schreibt, legt sie eine Kopie an:
`data/backup-<alte Version>-<Datum>/` (etwa `backup-1.0.x-20261028-160512/` mit `devices.json`,
`discovered.json` und – ab 1.1 – `state.json` und `settings.json`). Das passiert einmal je Versionswechsel. Eine
ältere Version kann die neuen Dateien zwar lesen, verwirft beim nächsten Speichern aber, was sie nicht kennt
(SecureOn, Weckschlüssel, Gruppen, Zeitpläne, eigene Ports, Verlauf). Zurück geht es so: wolweb stoppen, die
Dateien aus dem Sicherungsordner nach `data/` kopieren, Dateien, die in der Sicherung fehlen (bei 1.0.x
`state.json` und `settings.json`), aus `data/` löschen, dann die alte Version starten. Was seit dem Update
geändert wurde, fehlt danach. Weckverlauf, „zuletzt geweckt“ und „zuletzt online“ in der `state.json` der Sicherung
folgen ebenfalls den Einstellungen zur [Privatsphäre](#privatsphäre): wolweb löscht sie dort nach der
Aufbewahrungsdauer bzw. sofort, wenn die Aufzeichnung aus ist. Geräte, Einstellungen und Suchtreffer bleiben in der
Sicherung, wie sie beim Update waren – alte Sicherungsordner löschen, sobald du sie nicht mehr brauchst.

**Was sich mit 1.3 ändert – und ob du etwas tun musst:**

- *Passwort für Änderungen* (neu, freiwillig): Solange keins gesetzt ist, ändert sich nichts. Mit Passwort bleiben
  Ansehen und Wecken frei; Bearbeiten, Import und Export, die Netzwerksuche und die Einstellungsseite gibt es erst
  nach dem Entsperren. Automationen mit `auth.api_token` sind nicht betroffen, die Anmeldung per HTTP Basic
  entsperrt nicht. Siehe [Passwort für Änderungen](#passwort-für-änderungen).
- *Gruppen sind eigene Einträge:* `devices.json` bekommt eine Liste `groups` (Name, Reihenfolge, frühere Namen),
  Geräte ein Feld `order`. Vorhandene Gruppen werden alphabetisch übernommen, wie bisher angezeigt; neue Gruppen
  kommen jetzt ans Ende, und eine leere Gruppe bleibt, bis du sie löschst (bisher verschwand sie mit ihrem letzten
  Gerät). `GET /api/v1/groups` nennt auch leere Gruppen, in ihrer Reihenfolge. Eine ältere Version verwirft
  `groups` und `order` beim Speichern – die Sicherung (siehe oben) enthält den Stand vor dem Update.
- *Akzentfarbe* (neu): Standard `indigo` wie bisher.
- *Privatsphäre* (neu): `privacy.online_status` blendet den Online-Status aus, `privacy.wake_history`, wann Geräte
  geweckt wurden; Standard jeweils `all` wie bisher.
- *Verlauf verfällt* (neu): Weckverlauf, „zuletzt geweckt“ und „zuletzt online“ werden jetzt nach 30 Tagen gelöscht
  (`privacy.retention_days`, bis 365); `privacy.recording: "off"` speichert nichts davon. Beim ersten Start
  verschwinden ältere Einträge des Verlaufs – die Sicherung (siehe oben) enthält sie noch.
- *Hinter einem Reverse-Proxy:* Ohne Anmeldung prüft wolweb jetzt auch den Hostnamen, den der Proxy weiterreicht
  (`X-Forwarded-Host`, `Forwarded`). Ist das deine eigene Domain, erlaube sie wie in Schritt 1 beschrieben.
- *Prüfports:* `status.ports` nimmt höchstens 16 Ports (Doppelte fallen weg); mit mehr startet wolweb nicht.

**Was sich mit 1.2 ändert – und ob du etwas tun musst:**

- *Netzwerksuche ohne eingetragenes Netz:* Bisher war sie dann aus. Jetzt nimmt wolweb das eine direkt
  angeschlossene Heimnetz von selbst und sucht dort alle 30 Minuten (`discovery.interval`). Soll das nicht sein:
  `discovery.interval` auf `0` setzen (Einstellungsseite oder `WOLWEB_SCAN_INTERVAL=0`). Mit mehreren Netzen bietet
  die Suchleiste eine Auswahl an; ein eingetragenes Netz geht wie bisher vor.
- *Benachrichtigungen folgen keinen Weiterleitungen mehr:* Leitet die Webhook- oder ntfy-Adresse weiter (etwa
  `http://` → `https://`), trage die endgültige Adresse ein – „Testnachricht senden“ zeigt, ob es klappt.
- *Kein doppeltes Wecken:* Ein zweiter Weckruf für dasselbe Gerät innerhalb von 10 Sekunden wird nicht gesendet,
  sondern mit `"already": true` beantwortet – auf allen Wegen. Automationen, die absichtlich kurz hintereinander
  zweimal wecken, brauchen das nicht mehr (wolweb sendet jeden Weckruf ohnehin dreimal und wiederholt ihn bei
  Bedarf selbst).
- *Neue Felder in den Daten:* `schedule_paused` und `aliases` (frühere Namen) bei Geräten, dazu Angaben zum
  Ergebnis im Verlauf. Eine ältere Version verwirft sie beim Speichern – Pausen, alte Wecklinks und die Sätze im
  Verlauf gingen dann verloren; die Sicherung (siehe oben) enthält den Stand vor dem Update.
- *Meldungen der API:* sagen jetzt „Weckruf“ statt „Magic Packet“ (z. B. `"message": "Weckruf an NAS gesendet."`).
  Wer in einer Automation auf den genauen Text prüft, sollte auf `"success"` umstellen.
- *Standardsprache Englisch:* `language` steht jetzt auf `en`. Die Oberfläche folgt weiter dem Browser; englisch
  werden ohne Einstellung aber Benachrichtigungen (Webhook, ntfy) und Antworten an Aufrufe ohne Sprachangabe
  (curl, Home Assistant). Deutsch behalten: `"language": "de"` in `config.json`, `WOLWEB_LANGUAGE=de` oder
  „Standardsprache“ auf der Einstellungsseite. Das Log ist jetzt englisch, der Sicherungsordner heißt `backup-…`.
  Auch technische Statusfelder ändern sich: `method` lautet `tcp/445 (refused)` statt `tcp/445 (abgelehnt)`, und der
  Hinweis auf einen IP-Konflikt (`note`) ist immer englisch – Automationen mit solchen Textvergleichen anpassen.
- *Docker:* Die neue `docker-compose.yml` setzt `WOLWEB_NETWORKS` und `WOLWEB_SCAN_INTERVAL` nicht mehr (nur noch
  als Beispiel auskommentiert) – das Netz wird erkannt, und die Felder auf der Einstellungsseite sind nicht mehr
  gesperrt. Wer sein Netz dort fest vorgegeben hatte und das behalten will, übernimmt die Zeile in die neue Datei.
  Zeitpläne laufen in der Zeitzone des Containers (`TZ`, voreingestellt); weicht sie vom Browser ab, sagt das der
  Gerätedialog.

## Einstellungen

Das Zahnrad oben rechts öffnet die Einstellungsseite (`/settings`). Dort lassen sich die Netze und VLANs,
Netzwerksuche, Online-Prüfung, Wartezeiten, Benachrichtigungen (mit „Testnachricht senden“), öffentliche Wecklinks,
erlaubte Hostnamen, wer den Online-Status sieht und wann geweckt wurde ([Privatsphäre](#privatsphäre)), die
Standardsprache und die
Akzentfarbe ändern (17 Farben, jeweils hell und dunkel). Änderungen gelten sofort, ohne Neustart. Gruppen und das
Passwort für Änderungen haben dort eigene Bereiche, die sofort speichern.

- **Reihenfolge** (das Spätere gewinnt): Standardwerte, `config.json`, die Einstellungsseite, Umgebungsvariablen.
- **Gespeichert** wird in `data/settings.json` – im Datenverzeichnis, weil das immer beschreibbar ist (`config.json`
  ist in Docker oft schreibgeschützt eingebunden). Dort steht nur, was von `config.json` abweicht; spätere
  Änderungen an `config.json` wirken also weiter, solange man denselben Wert nicht auch auf der Seite geändert hat.
  „Auf config.json zurücksetzen“ löscht die Datei wieder.
- **Gesperrt**: Was per Umgebungsvariable gesetzt ist (in Docker etwa `WOLWEB_NETWORKS`), zeigt die Seite
  ausgegraut mit dem Namen der Variable. Wer es lieber auf der Seite pflegt, nimmt die Variable heraus.
- **Nicht auf der Seite**: Adresse, Port, Pfad, Datenverzeichnis, Nur-Lesen und die Zugangsdaten. Über die Seite
  könnte man sich sonst aussperren – oder ohne Anmeldung einfach eine setzen.
- Ohne Anmeldung und ohne [Passwort für Änderungen](#passwort-für-änderungen) kann jeder im Netz die Einstellungen
  sehen und ändern; die Seite weist darauf hin. Im Nur-Lesen-Modus lässt sie sich nur ansehen.

![Einstellungen: Netze und VLANs mit Schnittstellen des Rechners](docs/screenshots/settings.png)

### Passwort für Änderungen

Unten auf der Einstellungsseite lässt sich ein Passwort für Änderungen setzen – ohne Benutzernamen. Mit Passwort:

- **Ansehen und Wecken bleiben frei** für alle im Netz: Übersicht, Status, Weckknöpfe, Gruppen wecken und die festen
  Wecklinks.
- **Alles andere erst nach dem Entsperren**: Geräte anlegen, bearbeiten und löschen, Gruppen zuordnen, Import und
  Export, die Netzwerksuche („Geräte suchen“ und die Suchtreffer erscheinen erst nach dem Entsperren) und die
  Einstellungsseite. Das Schloss in der Kopfzeile fragt nach dem Passwort; danach bleibt dieser Browser entsperrt,
  bis er geschlossen wird oder 12 Stunden lang nichts bearbeitet wird (eine bloß offene Übersicht zählt nicht).
  Derselbe Knopf sperrt sofort wieder.
- Ändern oder Entfernen verlangt das bisherige Passwort; ein neues Passwort beendet die Entsperrung aller anderen
  Browser. Nach 5 Fehlversuchen aus einem Browser heißt es für ihn 30 s warten (mit jedem weiteren Fehlversuch
  doppelt so lange, höchstens 15 Minuten); hinter einem Reverse-Proxy unterscheidet der letzte Eintrag von
  `X-Forwarded-For` die Browser. Nach 30 Fehlversuchen insgesamt gilt die Wartezeit für alle, und es läuft immer nur
  eine Passwortprüfung zur Zeit.
- Gespeichert wird nur ein PBKDF2-SHA256-Hash in `data/state.json`; die Entsperrung ist ein signiertes Cookie
  (HttpOnly, SameSite=Strict).
- Automationen mit `auth.api_token` sind nicht betroffen. Die Anmeldung per HTTP Basic entsperrt nicht – die kennt
  oft der ganze Haushalt, das Passwort für Änderungen ist für weniger Leute gedacht.
- **Vergessen?** wolweb anhalten und einmal mit `-reset-password` starten: Das entfernt das Passwort und beendet sich.

  ```sh
  docker compose stop wolweb
  docker compose run --rm wolweb -reset-password
  docker compose start wolweb
  ```

  Ohne Docker: den Dienst anhalten, `wolweb -c config.json -reset-password` mit derselben Konfiguration ausführen
  (damit es das Datenverzeichnis findet) und den Dienst wieder starten.

### Privatsphäre

Unter „Privatsphäre“ auf der Einstellungsseite legst du fest, wer sieht, ob Geräte laufen (`privacy.online_status`):

| Auswahl | Wert | Wer sieht den Online-Status |
|---|---|---|
| Für alle anzeigen (Standard) | `all` | jeder, der wolweb aufruft |
| Nur nach dem Entsperren anzeigen | `unlocked` | nur Browser, die mit dem [Passwort für Änderungen](#passwort-für-änderungen) entsperrt sind, und Automationen mit `auth.api_token` – etwa zur Fehlersuche. Ohne Passwort für Änderungen sieht ihn niemand. |
| Für niemanden anzeigen | `none` | niemand, auch nicht nach dem Entsperren |

Ausgeblendet heißt überall, nicht nur in der Übersicht: Die Karten zeigen keinen Status, keinen grünen Punkt und
kein „Erneut wecken“, der Filter Online/Offline entfällt, und Gruppen nennen nur die Zahl ihrer Geräte. Der Server
lässt den Status aus `/api/v1/devices` und `/api/v1/groups`, den Live-Ereignissen, `/metrics` und dem Verlauf weg;
die `…/status`-Aufrufe antworten 403 mit `"hidden": true`. **Ausnahme Wecken:** Wer ein Gerät weckt – per Knopf in
der Übersicht oder über seinen Weck- oder Gruppenlink –, startet es ohnehin und sieht deshalb weiter, ob es
hochkommt. Die Karte in der Übersicht verfolgt den Start dieses einen Geräts und blendet den Status danach wieder
aus; Weck- und Gruppenseiten bestätigen den Start wie gewohnt. Die Weckantwort bringt dafür einen kurzlebigen
Schlüssel `watch` mit, den `…/status?watch=…` 15 Minuten lang für dieses Gerät bzw. diese Gruppe annimmt, und
`?wait=` funktioniert weiter. Nur die Vorabprüfung, ob das Gerät schon läuft, entfällt – sie verriete den Status,
ohne zu wecken. Auch die Suchtreffer verraten, wer im Netz ist; mit Passwort für Änderungen sehen sie nur
Entsperrte, egal wie eingestellt. Geprüft wird im Hintergrund weiter, für die automatische Wiederholung und die
Benachrichtigungen (deren Ereignisse wählst du getrennt). Die Anmeldung per HTTP Basic zählt
nicht als entsperrt, wie beim Passwort für Änderungen.

**„Zuletzt geweckt“ und Verlauf** (`privacy.wake_history`): eine eigene Auswahl unter dem Online-Status, mit denselben
drei Stufen und `all` als Standard – solange du nichts anderes wählst, ändert sich nichts. Sie blendet aus, wann
Geräte geweckt wurden: „geweckt vor …“ auf den Karten, den Knopf „Verlauf“ (`/api/v1/history` antwortet 403 mit
`"hidden": true`), `last_wake` in der API und die Kennzahlen `wolweb_device_last_wake_timestamp_seconds` und
`wolweb_wakes_total`. Bei `unlocked` sehen entsperrte Browser und das API-Token alles weiter. Das Wecken bleibt
unberührt: Wer ein Gerät weckt, verfolgt seinen Weckruf wie gewohnt, und der Verlauf wird weiter aufgezeichnet –
stellst du zurück, ist er wieder zu sehen.

**Aufzeichnung und Aufbewahrung** (`privacy.recording`, `privacy.retention_days`): Weckverlauf, „zuletzt geweckt“ und
„zuletzt online“ sind personenbezogen – wer wann ein Gerät geweckt hat, wann ein Arbeitsplatz lief. wolweb löscht sie
nach der eingestellten Zahl von Tagen (1 bis 365, Standard 30): Die Seiten lassen Älteres sofort weg, `state.json`
wird alle fünf Minuten bereinigt. Ohne Haken bei „Weckverlauf und „zuletzt online“ aufzeichnen“
(`recording: "off"`) löscht wolweb sie nach einer Rückfrage sofort und speichert ab dann nichts davon; der Knopf
„Verlauf“ entfällt. Wecken und Startbestätigung funktionieren in beiden Fällen weiter. Das Server-Log
(`docker logs wolweb`, `journalctl -u wolweb`) ist davon nicht betroffen – das führen Docker bzw. journald; dort lässt
es sich bei Bedarf begrenzen.

## Mehrere Netze und VLANs

Magic Packets sind Broadcasts, und die Netzwerksuche fragt per ARP – beides geht nicht über Router. wolweb braucht
deshalb in jedem Netz, in dem es wecken und suchen soll, **eine eigene Schnittstelle**: meist ein zusätzliches
Interface mit VLAN-Tag auf dem Rechner, auf dem wolweb läuft. Ist es angelegt, erscheint es in den Einstellungen
unter „Schnittstellen dieses Rechners“; „Übernehmen“ trägt das Netz ein, dazu ein Name („IoT“) und die VLAN-ID.
Geräte darin weckt wolweb über die richtige Schnittstelle (die Broadcast-Adresse folgt aus der IP des Geräts), und
die Suche findet sie. Für Geräte ohne IP schlägt der Gerätedialog unter „Erweitert → Broadcast-Adresse“ die
Broadcasts der eingetragenen Netze vor.
Bei mehreren Netzen am besten jedem Gerät eine IP oder Broadcast-Adresse geben: Ohne beides folgt sein Weckziel dem
Netz, in dem die Suche es zuletzt gesehen hat (ein Netzwechsel gilt erst nach einer Woche ohne Sichtung im alten).

Voraussetzung: Der Switch-Port bzw. die Bridge des Rechners reicht das VLAN getaggt durch (Trunk), und wolweb hat im
VLAN eine eigene, feste Adresse (außerhalb des DHCP-Bereichs oder per DHCP-Reservierung). **Kein Gateway** auf den
zusätzlichen Schnittstellen eintragen – die Standardroute bleibt im Hauptnetz.

**Proxmox (LXC):** Dem Container eine zweite Netzwerkkarte mit VLAN-Tag geben – in der Oberfläche unter
*Container → Netzwerk → Hinzufügen* (Bridge `vmbr0`, VLAN-Tag `20`, IPv4 statisch, Gateway leer) oder auf dem Host:

```sh
pct set 105 -net1 name=eth1,bridge=vmbr0,tag=20,ip=10.0.0.5/24
```

Die Bridge muss „VLAN aware“ sein (*Host → System → Netzwerk → vmbr0 → VLAN aware*, bzw. `bridge-vlan-aware yes` in
`/etc/network/interfaces`). Bei einer VM entsprechend eine weitere Netzwerkkarte mit VLAN-Tag hinzufügen.

**Debian, Raspberry Pi OS, Ubuntu (direkt oder als Docker-Host):** VLAN-Interface anlegen. Zum Ausprobieren:

```sh
sudo ip link add link eth0 name eth0.20 type vlan id 20
sudo ip addr add 10.0.0.5/24 dev eth0.20
sudo ip link set eth0.20 up
```

Dauerhaft mit NetworkManager (Raspberry Pi OS ab Bookworm, Ubuntu Desktop):

```sh
sudo nmcli con add type vlan con-name vlan20 dev eth0 id 20 \
  ipv4.method manual ipv4.addresses 10.0.0.5/24 ipv4.never-default yes ipv6.method disabled
```

mit ifupdown (Debian, Paket `vlan`) in `/etc/network/interfaces.d/vlan20`:

```
auto eth0.20
iface eth0.20 inet static
    address 10.0.0.5/24
```

mit netplan (Ubuntu Server) in `/etc/netplan/60-vlan20.yaml`, danach `sudo netplan apply`:

```yaml
network:
  version: 2
  vlans:
    vlan20:
      id: 20
      link: eth0
      addresses: [10.0.0.5/24]
```

**Docker:** Mit `network_mode: host` (wie in `docker-compose.yml`) sieht wolweb alle Schnittstellen des Hosts –
das VLAN-Interface also wie oben auf dem Host anlegen, am Container ändert sich nichts. Ohne Host-Netz geht es mit
je einem macvlan-Netz pro VLAN (`docker network create -d macvlan -o parent=eth0.20 --subnet 10.0.0.0/24 vlan20`),
das ist aber umständlicher. Docker Desktop (Windows, macOS) kann beides nicht.

**Absichern:** Eine Schnittstelle in einem abgeschotteten Netz (IoT, Gäste) macht wolweb auch von dort erreichbar.
Mit `host` in `config.json` (bzw. `WOLWEB_HOST`) die Weboberfläche nur an die Adresse im Hauptnetz binden, z. B.
`"host": "192.168.1.5"` – Wecken und Suche im VLAN funktionieren trotzdem.

Geht keine eigene Schnittstelle, zeigt die Einstellungsseite das Netz als „nicht direkt angeschlossen“ und weckt
Geräte darin per gerichtetem Broadcast (z. B. `10.0.0.255:9`). Ankommen wird er nur, wenn der Router ihn ins
VLAN weiterleitet – bei OPNsense und pfSense etwa mit dem Plugin „UDP Broadcast Relay“; die meisten Heimrouter
können das nicht, und suchen lässt sich dort ohnehin nicht.

**Einzelne Geräte hinter dem Router (Unicast):** Für ein einzelnes Gerät in einem anderen Netz geht es auch ohne
eigene Schnittstelle und ohne Broadcast-Weiterleitung: wolweb schickt den Weckruf direkt an die IP-Adresse des
Geräts, und der Router leitet ihn weiter wie jedes andere Paket. Damit das auch klappt, während das Gerät schläft,
braucht der Router drei Dinge:

1. eine **feste IP-Adresse** für das Gerät (DHCP-Reservierung),
2. einen **statischen ARP-Eintrag** (IP-Adresse → MAC-Adresse des Geräts) im Netz des Geräts. Ein schlafendes Gerät
   beantwortet keine ARP-Anfragen; ohne festen Eintrag verwirft der Router den Weckruf, sobald er die MAC-Adresse
   vergessen hat – meist schon wenige Minuten nach dem Einschlafen. Bei OPNsense unter *Interfaces → Neighbors*
   (deutsch *Schnittstellen → Nachbarn*), bei pfSense über die Option für einen statischen ARP-Eintrag in der
   DHCP-Reservierung. Viele Heimrouter bieten das nicht an.
3. eine **Firewall-Regel** vom wolweb-Rechner zum Gerät für UDP an Port 9 und 7 (wolweb sendet an beide), dazu Ping
   oder einen der geprüften Ports, damit wolweb den Start bestätigen kann.

In wolweb dann beim Gerät unter „Erweitert → Broadcast-Adresse“ die IP-Adresse des Geräts mit Port eintragen, z. B.
`10.0.0.20:9`. Die Netzwerksuche findet solche Geräte nicht – die MAC-Adresse also von Hand eintragen (sie steht in
den Netzwerkeinstellungen des Geräts oder in der ARP- bzw. DHCP-Übersicht des Routers). Als gestartet gilt das Gerät
hier erst, wenn es auf Ping antwortet oder eine Verbindung annimmt (siehe [Feste Wecklinks](#feste-wecklinks)).

## Konfiguration

`config.json` (siehe `config.example.json`) – alles hat Standardwerte, die Datei darf fehlen. Was auf der
[Einstellungsseite](#einstellungen) geändert wurde, gilt vor der Datei; Umgebungsvariablen gehen allem vor. Wo zwei
Variablennamen stehen, ist der erste der des ursprünglichen wolweb – beide funktionieren.

| Schlüssel | Variable | Standard | Bedeutung |
|---|---|---|---|
| `host`, `port` | `WOLWEBHOST` / `WOLWEB_HOST`, `WOLWEBPORT` / `WOLWEB_PORT` | `0.0.0.0`, `8089` | Adresse der Weboberfläche |
| `vdir` | `WOLWEBVDIR` / `WOLWEB_VDIR` | `/` | Unterpfad, z. B. `/wolweb` hinter einem Reverse-Proxy |
| `bcastip` | `WOLWEBBCASTIP` / `WOLWEB_BCASTIP` | automatisch | Standardziel, wenn weder Gerät noch Netz eins liefern |
| `read_only` | `WOLWEBREADONLY` / `WOLWEB_READONLY` | `false` | nur wecken, nichts ändern |
| `language` | `WOLWEB_LANGUAGE` | `en` | `en` oder `de`: Sprache, wenn weder Umschalter noch Browser eine nennen (auch für Benachrichtigungen) |
| `accent` | `WOLWEB_ACCENT` | `indigo` | Akzentfarbe der Oberfläche (Knöpfe, Auswahl, Markierungen) auf allen Seiten: indigo, blue, sky, cyan, teal, emerald, green, lime, amber, orange, red, rose, pink, fuchsia, purple, violet, slate – am einfachsten auf der Einstellungsseite gewählt |
| `data_dir` | `WOLWEB_DATA_DIR` | `data` | Geräte (`devices.json`), Laufzeitdaten (`state.json`), Suchergebnisse (`discovered.json`) und Änderungen der Einstellungsseite (`settings.json`) |
| `discovery.networks` | `WOLWEB_NETWORKS` | automatisch | Netze (CIDR, Variable kommagetrennt); leer = das eine Heimnetz automatisch, bei mehreren Auswahl in der Oberfläche; auch mit Namen: `{"cidr": "10.0.0.0/24", "name": "IoT", "vlan": 20, "scan": false}` – `scan: false` heißt anzeigen, aber nicht durchsuchen |
| `discovery.interval` | `WOLWEB_SCAN_INTERVAL` | `30m` | automatische Suche, `0` = aus (mindestens `1m`) |
| `discovery.max_hosts` | – | `1024` | größtes erlaubtes Segment |
| `status.interval` | `WOLWEB_STATUS_INTERVAL` | `30s` | Online-Prüfung aller Geräte, `0` = aus (mindestens `5s`) |
| `status.ports` | – | 22, 80, 443, 445, … | Ports für die Online-Prüfung (höchstens 16) |
| `status.wake_timeout` | `WOLWEB_WAKE_TIMEOUT` | `3m` | so lange wartet die Weckseite (`5s` bis `10m`) |
| `status.wake_retry` | `WOLWEB_WAKE_RETRY` | `45s` | antwortet ein geweckter Rechner so lange nicht, geht der Weckruf einmal erneut raus; `0` = nie |
| `auth.username/password` | `WOLWEB_USER`, `WOLWEB_PASSWORD` | – | Anmeldung (HTTP Basic) |
| `auth.api_token` | `WOLWEB_TOKEN` | – | `Authorization: Bearer …` für Automationen |
| `auth.public_wake` | `WOLWEB_PUBLIC_WAKE` | `true` | Wecklinks funktionieren ohne Anmeldung |
| `auth.wake_keys` | `WOLWEB_WAKE_KEYS` | `false` | Wecklinks ohne Anmeldung nur mit dem Schlüssel des Geräts (`?key=…`) |
| `allowed_hosts` | `WOLWEB_ALLOWED_HOSTS` | – | weitere Hostnamen, unter denen wolweb erreichbar ist (siehe Sicherheit) |
| `notify.webhook` | `WOLWEB_WEBHOOK` | – | URL, die jedes Ereignis als JSON per POST bekommt |
| `notify.ntfy` | `WOLWEB_NTFY` | – | ntfy-Thema, z. B. `https://ntfy.sh/mein-geheimes-thema` |
| `notify.events` | `WOLWEB_NOTIFY_EVENTS` | alle | Auswahl aus `online`, `offline`, `wake`, `wake_failed` |
| `privacy.online_status` | `WOLWEB_ONLINE_STATUS` | `all` | wer sieht, ob Geräte laufen: `all`, `unlocked` (nur nach dem Entsperren) oder `none` (siehe [Privatsphäre](#privatsphäre)) |
| `privacy.wake_history` | `WOLWEB_WAKE_HISTORY` | `all` | wer sieht, wann Geräte geweckt wurden („geweckt vor …“, Verlauf): `all`, `unlocked` oder `none` |
| `privacy.recording` | `WOLWEB_RECORDING` | `on` | `off` = keinen Weckverlauf, kein „zuletzt geweckt“ und kein „zuletzt online“ speichern (Vorhandenes wird gelöscht) |
| `privacy.retention_days` | `WOLWEB_RETENTION_DAYS` | `30` | nach wie vielen Tagen Weckverlauf, „zuletzt geweckt“ und „zuletzt online“ gelöscht werden (1–365) |

`devices.json` enthält nur, was man selbst einträgt; was wolweb beobachtet (zuletzt geweckt, zuletzt online, bis
zu 500 Weckversuche der letzten `privacy.retention_days` Tage) steht in `state.json`. Der Export unten auf der Seite sichert die Geräte samt
Wecklink-Schlüsseln. Für eine vollständige Sicherung oder einen Umzug wolweb stoppen und das ganze Datenverzeichnis
und die `config.json` kopieren – und die Umgebungsvariablen aufheben, mit denen wolweb eingestellt ist.

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
Umleitung (3xx), geht die Nachricht nicht weiter, sondern es steht ein Fehler im Log. Sonst könnte ein umgeleiteter
Server die Nachricht an ein Ziel schicken lassen, das niemand eingetragen hat. Abhilfe: die endgültige Adresse direkt
eintragen (z. B. mit `https://` statt `http://`). „Testnachricht senden“ sagt nur „zugestellt“ oder „nicht
zugestellt“ – den Grund nennt das Log (`docker logs wolweb` bzw. `journalctl -u wolweb`). Technische Einzelheiten zu
internen Zielen zeigt die Seite damit nicht; ob eine Adresse die Nachricht angenommen hat, sieht natürlich weiterhin,
wer die Einstellungsseite benutzen darf.

`/metrics` liefert Kennzahlen im Prometheus-Format (`wolweb_device_up`, `wolweb_device_rtt_seconds`,
`wolweb_device_last_seen_timestamp_seconds`, `wolweb_device_last_wake_timestamp_seconds`, `wolweb_wakes_total`,
`wolweb_discovery_found` …). Mit Anmeldung braucht auch der Abruf sie – in Prometheus
`authorization: {credentials: <api_token>}`. Ist der Online-Status ausgeblendet ([Privatsphäre](#privatsphäre)),
bleiben `wolweb_device_up`, `wolweb_device_rtt_seconds` und `wolweb_device_last_seen_timestamp_seconds` leer – bei
`unlocked` bringt das Token sie zurück. `privacy.wake_history` macht dasselbe mit
`wolweb_device_last_wake_timestamp_seconds` und `wolweb_wakes_total`. Automatische Wiederholungen zählen in beiden nicht mit.

## Sicherheit

- **Anmeldung**: `auth.username`/`auth.password` (HTTP Basic, im Internet nur hinter HTTPS) und/oder `auth.api_token`
  für Automationen.
- **Anmeldung**: Zugangsdaten (HTTP Basic oder API-Token) werden nacheinander geprüft, ein Fehlversuch hält die
  Prüfung 0,5 s auf – insgesamt höchstens zwei pro Sekunde; Fehlversuche stehen im Log (höchstens einmal pro Minute).
  Wer sich in den letzten 7 Tagen angemeldet hat, wird sofort geprüft – ein Angriff sperrt diese Aufrufer nicht aus;
  eine erste Anmeldung muss während eines Angriffs unter Umständen warten (bis etwa 16 s, oder „gleich noch einmal
  versuchen“), öffentliche Weckseiten bleiben erreichbar. Trotzdem ein langes Passwort und ein langes zufälliges Token
  nehmen; wolweb warnt beim Start, wenn sie kurz sind.
- **Passwort für Änderungen**: Ansehen und Wecken bleiben frei, Ändern erst nach dem Entsperren – siehe
  [Passwort für Änderungen](#passwort-für-änderungen).
- **Online-Status und Weckverlauf**: Wer sieht, ob Geräte laufen und wann sie geweckt wurden – alle, nur nach dem
  Entsperren oder niemand –, siehe [Privatsphäre](#privatsphäre).
- **Öffentliche Wecklinks** (`auth.public_wake`): Mit Anmeldung bleiben `/wake/<Name>` und die Weckseite ohne Login
  erreichbar. Damit niemand Geräte über erratene Namen weckt, verlangt `auth.wake_keys` zusätzlich einen Schlüssel
  je Gerät – „Wecklink kopieren“ hängt ihn an (`/wake/NAS?key=…`), im Gerätedialog lässt er sich erneuern (Gruppenschlüssel im Gruppendialog). Eine gelöschte und gleichnamig neu angelegte Gruppe bekommt einen neuen Schlüssel.
- **Kein doppeltes Wecken**: Jedes Gerät bekommt höchstens alle 10 s einen Weckruf – mit und ohne Anmeldung, über
  Wecklink, Gruppenlink, Weckseite und API gemeinsam. Ein zweiter Aufruf in dieser Zeit (zweimal getippt, Seite neu
  geladen) ist kein Fehler: Er meldet „wird bereits geweckt“ (`"already": true`) und die Seite verfolgt den
  laufenden Start, ohne erneut zu senden. Scheitert das Senden, bekommen weitere Aufrufe binnen 3 s denselben Fehler,
  ohne erneut zu senden. Zeitpläne und die automatische Wiederholung werden nie gebremst.
- **Wecklinks von fremden Seiten**: Ein Wecklink weckt beim Öffnen – mit Absicht, auch wenn er auf einer anderen
  Seite steht (Home-Assistant-Dashboard, Glance, Lesezeichenseite). Das heißt aber auch: Jede Webseite, die du
  besuchst, kann deinen Browser zu `/wake/<Name>` oder `/wake-group/<Name>` schicken, und die ausgelieferte
  Weckseite weckt dann. Mehr als Wecken ist so nicht möglich, und `HEAD` (Linkvorschauen in Messengern, Linkprüfer)
  weckt nie. Wer auch das verhindern will, schaltet die Anmeldung samt `auth.wake_keys` ein: Dann braucht jeder
  Link – Gerät wie Gruppe gleichermaßen – den Schlüssel, den „Wecklink kopieren“ anhängt, und Namen lassen sich
  nicht erraten. Ein bei wolweb angemeldeter Browser braucht keinen Schlüssel; schickt ihn eine fremde Webseite
  ohne Schlüssel auf einen Wecklink, weckt die Weckseite erst auf Knopfdruck. Das ist eine bewusste Ausnahme vom Schutz gegen Cross-Site-Anfragen: Wecklinks sollen von überall
  funktionieren, jede andere Änderung braucht die Seite von wolweb selbst oder ein Token.
- **DNS-Rebinding**: Ohne Anmeldung nimmt wolweb nur Aufrufe unter IP-Adressen, Namen ohne Punkt (`nas`), lokalen
  Endungen (`.lan`, `.local`, `.home`, `.internal`, `.home.arpa`, `.fritz.box` …) und den Namen aus
  `allowed_hosts` an (`"wol.example.com"`, `"*.example.com"`, `"*"` schaltet die Prüfung ab). Sonst könnte eine
  fremde Webseite ihren Namen auf die IP von wolweb umbiegen und die API im Browser fernsteuern. Mit Anmeldung wird
  nur geprüft, wenn `allowed_hosts` gesetzt ist. Hinter einem Reverse-Proxy soll dieser den ursprünglichen Hostnamen
  weitergeben (nginx: `proxy_set_header Host $host;`, Apache: `ProxyPreserveHost On`); Namen, die ein Proxy in
  `X-Forwarded-Host` oder `Forwarded: host=` meldet, müssen ebenfalls erlaubt sein.

## Sprache

Die Oberfläche gibt es auf Deutsch und Englisch. Gewählt wird in dieser Reihenfolge: `?lang=de|en` in der Adresse,
der Umschalter oben rechts (Cookie `wolweb_lang`), die Sprache des Browsers (`Accept-Language`), sonst `language`.
Das gilt auch für die API – Automationen bekommen deutsche Meldungen mit `Accept-Language: de` oder `?lang=de`. Fehlerantworten
nennen zusätzlich das betroffene Feld (`"field": "mac"`). Die Texte stehen in `internal/i18n/de.json` und
`en.json`; ein Test prüft, dass beide dieselben Schlüssel haben und alles Benutzte vorhanden ist. Das Protokoll
(Log) ist englisch.

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
- **Sortieren ohne Ziehen**: Unter „Sortieren“ hat jede Gruppe und jedes Gerät auch Pfeilknöpfe; der Fokus bleibt
  auf dem verschobenen Eintrag, und Screenreader sagen seine neue Position an.
- **Screenreader**: Knöpfe nennen das Gerät, zu dem sie gehören; Weck- und Gruppenseite sagen die Phasen an
  (sendet, wartet, online); Filter melden, welcher aktiv ist.
- **Kontrast** nach WCAG AA (4,5:1) in hell und dunkel, mit jeder Akzentfarbe; Animationen entfallen bei „Bewegung
  reduzieren“.

Die Browsertests (siehe [Entwicklung](#entwicklung)) prüfen das bei jedem Push: [axe-core](https://github.com/dequelabs/axe-core)
nach WCAG 2.2 AA auf allen Seiten und Dialogen in hell und dunkel, die Tastaturbedienung und die Größe aller
Touch-Ziele auf einem Handy.

## API

Alle ändernden Aufrufe brauchen den Kopf `X-WolWeb: 1` (Schutz gegen Cross-Site-Requests) oder ein Bearer-Token;
fehlt er, kommt 403 mit einer Meldung für Menschen und dem technischen Grund in `"error"`. Mit
[Passwort für Änderungen](#passwort-für-änderungen) bekommen ändernde Aufrufe ohne Entsperrung oder Token 403 mit
`"locked": true`. Ist der Online-Status ausgeblendet ([Privatsphäre](#privatsphäre)), kommen Listen und Ereignisse
ohne Status, `…/status` antwortet 403 mit `"hidden": true`, und die Antwort auf einen Weckaufruf trägt den
`watch`-Schlüssel, mit dem sich der Start verfolgen lässt. Mit `privacy.wake_history` fehlt `last_wake`, und
`/api/v1/history` antwortet ebenfalls 403 mit `"hidden": true`.

| Methode | Pfad | |
|---|---|---|
| GET | `/api/v1/info` | Einstellungen, lokale Netze |
| GET | `/api/v1/events` | Live-Ereignisse (Server-Sent Events): `status`, `devices`, `scan` |
| GET / POST | `/api/v1/devices` | Liste (mit Status) / anlegen |
| POST | `/api/v1/devices/group` | mehrere Geräte einer Gruppe zuordnen: `{"ids": […], "group": "Homelab"}` (leer = aus der Gruppe nehmen) |
| PUT / DELETE | `/api/v1/devices/{id}` | ändern (`updated_at` mitschicken schützt vor Überschreiben) / löschen |
| POST | `/api/v1/devices/{id}/wake` | Magic Packet senden |
| GET | `/api/v1/devices/{id}/status` | sofort prüfen |
| POST | `/api/v1/devices/{id}/wake-key` | neuen Weckschlüssel erzeugen |
| GET | `/api/v1/schedule/preview?s=…` | Zeitplan prüfen, ohne zu speichern: einheitliche Schreibweise, nächste Weckzeit, Zeitzone des Servers |
| GET | `/api/v1/groups` | Gruppen in ihrer Reihenfolge, auch leere, mit Mitgliedern (und Schlüssel des Gruppen-Wecklinks) |
| POST | `/api/v1/groups` | Gruppe anlegen: `{"name": "Büro", "members": [IDs]}` (Mitglieder optional) |
| PUT / DELETE | `/api/v1/groups/{name}` | umbenennen und/oder Mitglieder setzen (`{"name": …, "members": […]}`, ohne `members` bleiben sie) / löschen (die Geräte bleiben, ohne Gruppe) |
| PUT | `/api/v1/order` | Reihenfolge wie in der Übersicht: `{"groups": [Namen], "devices": [{"group": "Büro", "ids": […]}, {"group": "", "ids": […]}]}` – steht ein Gerät bei einer anderen Gruppe, wechselt es dorthin |
| POST | `/api/v1/groups/{name}/wake` | alle Geräte einer Gruppe wecken |
| GET | `/api/v1/history` | Weckverlauf, neueste zuerst (`?limit=`) |
| GET / POST | `/api/v1/export`, `/api/v1/import` | Geräte als JSON herunterladen / aus einer Datei übernehmen; die Antwort nennt jeden Eintrag (`items`: `added` – ggf. mit `new_name` –, `skipped`, `error` mit Grund), die Oberfläche zeigt die vollständige Liste |
| GET | `/api/v1/discovery` | letzte Suche und Treffer |
| POST | `/api/v1/discovery/scan`, `/api/v1/discovery/cancel` | Suche starten / abbrechen |
| POST | `/api/v1/discovery/{mac}/ignore` | `{"ignored":true}` |
| DELETE | `/api/v1/discovery/{mac}` | Suchtreffer entfernen |
| POST | `/api/v1/discovery/network` | für die Suche gewähltes Netz speichern |
| GET / PUT / DELETE | `/api/v1/settings` | Einstellungen der Einstellungsseite lesen / ändern / zurücksetzen |
| POST | `/api/v1/settings/test-notify` | Testbenachrichtigung senden |
| POST | `/api/v1/unlock`, `/api/v1/lock` | diesen Browser mit `{"password": "…"}` entsperren (setzt ein Cookie) / wieder sperren |
| PUT | `/api/v1/password` | Passwort für Änderungen setzen, ändern oder entfernen: `{"current": "…", "password": "…"}` (leeres `password` entfernt es) |
| GET | `/wake/{name}`, `/health`, `/data/get` | wie im Original |
| GET / POST | `/wake-group/{name}` | Gruppen-Wecklink |
| GET | `/wake/{name}/status`, `/wake-group/{name}/status` | Status für Weck- und Gruppenseite (`?watch=…` bei ausgeblendetem Online-Status) |
| GET | `/metrics` | Kennzahlen für Prometheus |

Meldungen folgen der Sprache der Anfrage (siehe [Sprache](#sprache)); mit `Accept-Language: de` antwortet ein
Wecklink z. B. `{"success":true,"message":"Weckruf an NAS gesendet."}`.

## Entwicklung

```sh
go test ./...
go run . -c config.json
```

Die Oberfläche liegt in `internal/web/` (Vorlagen und statische Dateien, eingebettet per `go:embed`).
Herstellerliste aktualisieren: `sh tools/gen-oui.sh`. Code-Kommentare sind überwiegend deutsch, ältere Commit-Texte
ebenso; Issues und Pull Requests auf Englisch sind willkommen.

Browsertests (Playwright mit Chromium, braucht Go und Node.js) liegen in `e2e/`. Sie bauen wolweb selbst, starten
es mit leerem Datenverzeichnis auf Port 8399 und prüfen Übersicht, Gerätedialog,
Suche und Filter, Sprache, einfache Ansicht, Sortieren, Gruppen, das Passwort für Änderungen, die Privatsphäre, die
Einstellungsseite, Weck- und Gruppenseite mit allen Ausgängen samt automatischer Wiederholung sowie die
Barrierefreiheit. Vom echten Netz bleibt der Testserver getrennt: Weckrufe gehen an 127.0.0.1, die automatische
Netzwerksuche ist aus, und die erfundenen Suchtreffer liegen auf 127.0.0.x. Nur das „offline“-Testgerät wird im
Dokumentationsnetz 198.51.100.0/24 geprüft – dort gibt es kein echtes Gerät, und eine Adresse auf dem Rechner selbst
antwortete immer:

```sh
cd e2e
npm ci
npx playwright install chromium
npx playwright test
```

Die Screenshots in `docs/screenshots/` erzeugt `node screenshots.js` (im Ordner `e2e`) neu – mit erfundenen
Geräten, Adressen aus 192.168.1.x / 10.0.0.x und lokal verwalteten MACs, nie mit echten Daten des Rechners.

GitHub Actions prüft jeden Push und Pull Request (gofmt, `go vet`, `go test -race`, Builds für Linux amd64/arm64/
armv7, Windows, macOS, Browsertests, Docker-Image für drei Plattformen). Ein Tag `v*` (`git tag v1.3.0 && git push --tags`)
erzeugt ein Release mit fertigen Programmen und das Image auf ghcr.io – das Paket danach einmal unter
„Packages“ auf öffentlich stellen.

## Lizenz

MIT – siehe [LICENSE](LICENSE). Die eingebettete Herstellerliste stammt aus Wiresharks
[`manuf`](https://www.wireshark.org/download/automated/data/manuf) (Daten des IEEE-Registers).
