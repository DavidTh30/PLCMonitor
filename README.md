# PLC Monitor

PLC Monitor (`plcmonitor`) monitors a PLC (300/400/1200/1500-series CPUs) over
ISO-TCP (port 102), or any **Modbus TCP / Modbus RTU** device. It:

- polls tags on a fixed interval: data block (DB), input (I), output (Q) and
  marker (M) areas, or Modbus holding registers, input registers, coils and discrete
  inputs (BOOL, BYTE/USINT, SINT, WORD/UINT, INT, DWORD/UDINT, DINT, REAL, LREAL),
- shows live values in a table, with a **live chart** for any tag,
- has a **File menu** to keep network and tag configurations in separate
  files (New, Open, Save, Save as),
- logs every value change to the console,
- exports changes to **CSV**, **MQTT** and/or **InfluxDB**,
- reconnects on its own when the link drops (exponential backoff), marking
  tags as bad while the PLC is unreachable,
- has a built-in **simulator** so you can try it without a PLC.

PLC Monitor was called S7 Monitor up to version 1.2.1. The installer moves an
existing installation, with its settings, tag files and trend history, to the
new folder.

It uses the pure-Go libraries [`github.com/robinson/gos7`](https://github.com/robinson/gos7)
and [`github.com/simonvetter/modbus`](https://github.com/simonvetter/modbus),
so there is no cgo, no Snap7 DLL and no serial driver to install.

The same code builds two programs:

| File | What it is |
|---|---|
| **`PLCMonitorApp.exe`** | Standalone Windows desktop app: its own window, no console, no browser needed. |
| `plcmonitor.exe` | Console program; the dashboard opens in a web browser and can be reached from other machines. |

## Build

Requires Go 1.27 (the version in `go.mod`). No C compiler is needed.

```bash
./build.ps1
```

This runs `go vet` and builds, all with the version number (default 1.0.0;
`./build.ps1 -Version 1.2.0` for another):

- for Windows 64-bit (x64): `PLCMonitorApp.exe`, `plcmonitor.exe` and the
  installer `PLCMonitorSetup-<version>-x64.exe`;
- for Windows 32-bit (x86): `PLCMonitorApp-x86.exe`, `plcmonitor-x86.exe` and
  `PLCMonitorSetup-<version>-x86.exe`;
- for Linux: `plcmonitor-linux-amd64`, `-386`, `-arm64` and `-armv7`.

Each installer holds its Windows programs and the Linux builds for the DCP
agent (amd64, arm64, armv7).

To build the Windows programs by hand (in the project folder):

```bash
go build -tags gui -ldflags "-H windowsgui -s -w" -o PLCMonitorApp.exe .
```

```bash
go build -o plcmonitor.exe .
```

## Installing on Windows (PLCMonitorSetup.exe)

Run `PLCMonitorSetup-<version>-x64.exe` (or `-x86.exe` on 32-bit Windows); all program files are inside.

- **For the current user, no administrator rights needed.** The default
  folder is `%LOCALAPPDATA%\Programs\PLC Monitor`; **Browse…** picks another.
  The folder must be writable, because PLC Monitor keeps its settings and
  history next to the program. Setup refuses one that isn't, such as Program
  Files.
- **Installed:**
  - `PLCMonitorApp.exe`, `plcmonitor.exe`, `README.md`;
  - `linux\plcmonitor-linux-amd64` and `-arm64` (to copy to a Linux PC for the
    PROFINET agent);
  - a Start menu shortcut, and a desktop shortcut (optional);
  - an entry in **Settings → Apps → Installed apps**;
  - `Uninstall PLC Monitor.exe`.
- **Start PLC Monitor when setup is done** is ticked by default.
- **Update:** run a newer setup. If PLC Monitor is running from that folder,
  setup closes it first: it asks the window to close, so data is saved, and
  ends the program only if it hasn't closed after 8 s. Your `config.yaml`,
  network and tag files, `changes.csv`, logs and trend history are never
  overwritten.
- **Uninstall** (Installed apps, or the uninstaller): removes the program and
  shortcuts and keeps your settings, tag files and history. Tick *Also delete
  my settings…* to remove the whole folder. The uninstaller deletes itself.
- **Silent (for rolling out to many PCs):**
  - `PLCMonitorSetup-1.0.0.exe /S` installs to the default folder;
  - `/S /nodesktop /D=C:\Tools\PLC Monitor` installs elsewhere (`/D=` comes
    last, and spaces are fine);
  - `"Uninstall PLC Monitor.exe" /S` uninstalls and keeps data; add `/purge` to
    delete it too.

  The exit code is 0 on success.
- The setup window uses the same WebView2 runtime as the app. Without it, a
  message says where to get it; the silent mode works without it.
- The setup program is **not code-signed**, so Windows SmartScreen may warn
  ("Windows protected your PC" → *More info* → *Run anyway*). Signing it needs
  a code-signing certificate, which is a business decision.

## Desktop app (PLCMonitorApp.exe)

`PLCMonitorApp.exe` is a single file. You can install it with the setup above,
or copy it to any folder and double-click it.

- **First run:** if there is no `config.yaml` next to the .exe, the app creates
  one from the example, with the **simulator off** and no PLC address, so it
  connects to nothing yet and says so. Open **Network settings** and enter the
  PLC address, rack and slot. To try the program without a PLC, tick *Use the
  built-in simulator* there instead.
- **Files:** `config.yaml`, `changes.csv` and the log `plcmonitor.log` all live
  next to the .exe. The folder must be writable, so use something like
  `C:\PLCMonitor` rather than `C:\Program Files`. To use another config, start
  it as `PLCMonitorApp.exe -config D:\plant\line1.yaml`; the other files then go
  next to that config.
- **Window:** the title bar and taskbar show the target and link state, e.g.
  `PLC Monitor - 192.168.0.1 rack 0 slot 1 (connected)`. Closing the window
  stops monitoring and flushes the exporters.
- **Errors:** a broken config or similar startup error is shown in a message
  box. Everything else goes to `plcmonitor.log`, which is restarted as
  `plcmonitor.log.old` once it passes 10 MB.
- **Network access:** the window talks to a private web server on a random
  `127.0.0.1` port, so nothing is reachable from the network. `web.listen` in
  the config is ignored. Pass `-listen :8080` if you also want the browser
  dashboard for other PCs.
- **Requirement:** the window uses the Microsoft Edge **WebView2 Runtime**,
  which is part of Windows 11 and current Windows 10. If it is missing, the app
  says so and links to the installer. Browser data is kept in
  `%LOCALAPPDATA%\PLCMonitor\WebView2`.
- **Icon:** both .exe files carry the PLC Monitor icon, so Explorer, desktop
  shortcuts, the taskbar and Alt+Tab all show it. The browser tab shows it
  too. The source picture is `winres/icon.png`. `build.ps1` runs
  `go run ./tools/mkres`, a small generator in this project that needs only
  Go's standard library. It turns the PNG into icon sizes 16–256 px plus a
  Windows manifest (DPI-aware, runs without admin rights) in
  `rsrc_windows_amd64.syso`, which `go build` links in automatically. It also
  writes `winres/plcmonitor.ico` for shortcuts or installers. To change the
  icon, replace `winres/icon.png` (256×256) and run `build.ps1`. If Explorer
  still shows the old icon, that is its icon cache; it updates after a while
  or after renaming the file.

## Quick start without a PLC (console version)

```bash
./plcmonitor.exe -simulate
```

Open <http://localhost:8080/>. The simulator writes changing values into the
configured tag addresses: BOOLs toggle, integers count, and REALs follow a sine
wave. It drops the link every 45 s for 6 s (`simulator.drop_every` /
`down_for`), so you can watch the reconnect logic and the bad-quality display.
Changes are appended to `changes.csv`.

## Running against a real PLC

1. Edit `config.yaml`: set `plc.address`, `rack`, `slot` and your tags.
2. Run:

```bash
./plcmonitor.exe -config config.yaml
```

Flags:

| Flag | Meaning |
|---|---|
| `-config path` | config file (default `config.yaml`) |
| `-simulate` | use the simulator, overriding `simulate:` in the file |
| `-listen addr` | dashboard address, e.g. `:9000` or `127.0.0.1:8080` |

Stop it with Ctrl+C. Exporters are flushed and closed on shutdown.

### PLC-side settings (needed for 1200/1500-series CPUs)

1200/1500-series CPUs block this kind of access by default. In the PLC
engineering software:

- CPU properties → *Protection & Security* → *Connection mechanisms*: enable
  **"Permit access with PUT/GET communication from remote partner"**. Set the
  access level to one that allows reading (e.g. *Full access* or *Read access*).
- For every DB you read: block properties → *Attributes*: **uncheck
  "Optimized block access"**, then recompile and download. Optimized DBs have no
  fixed byte offsets, so absolute addressing (`offset`) cannot reach them.
- Use the offsets shown in the DB's *Offset* column after compiling.

300/400-series CPUs need no extra settings.

### Rack and slot

| CPU | rack | slot |
|---|---|---|
| 1200-series CPU | 0 | 1 |
| 1500-series CPU | 0 | 1 (some setups use 0) |
| 300-series CPU | 0 | 2 |
| 400-series CPU | 0 | the CPU's slot in the rack (check HW Config) |

`connection_type` is `pg` (default), `op` or `basic`. Change it only if the CPU
runs out of PG connections.

## Configuration reference

All durations use Go syntax (`500ms`, `1s`, `2m`). See `config.yaml` for a
complete example. (The value `s7` selects the ISO-TCP protocol; it keeps this
name so existing configuration files keep working.)

```yaml
protocol: s7             # s7 (ISO-TCP, the default) | modbus | opcua
plc:
  address: 192.168.0.1   # host or host:port (port 102 if omitted)
  rack: 0
  slot: 1
  connection_type: pg    # pg | op | basic
  timeout: 5s            # connect and per-request timeout

poll_interval: 1s
reconnect_delay: 2s      # first retry; doubles on each failure
reconnect_max: 30s       # backoff ceiling

simulate: false
simulator:
  drop_every: 45s        # 0 = never drop
  down_for: 6s

web:
  listen: ":8080"        # "" disables the dashboard
```

### Tags

```yaml
tags:
  - name: Tank_Level     # unique name; used in logs, CSV, MQTT topic, Influx tag
    area: DB             # DB | I | Q | M   (E/PE, A/PA, MK also accepted)
    db: 1                # DB number (DB area only)
    offset: 4            # byte offset
    bit: 0               # 0..7, BOOL only
    type: REAL           # see list below
    deadband: 0.5        # optional: ignore numeric changes smaller than this
    description: "%"     # optional, shown on the dashboard
```

| Type | Size | Shown as |
|---|---|---|
| BOOL | 1 bit | TRUE/FALSE |
| BYTE, USINT | 1 byte | 0..255 |
| SINT | 1 byte | -128..127 |
| WORD, UINT | 2 bytes | 0..65535 |
| INT | 2 bytes | -32768..32767 |
| DWORD, UDINT | 4 bytes | unsigned 32-bit |
| DINT | 4 bytes | signed 32-bit |
| REAL | 4 bytes | IEEE float |
| LREAL | 8 bytes | IEEE double (1200/1500-series CPUs only) |

Tag addresses map to absolute addresses like this:

| Config | Absolute address |
|---|---|
| `area: DB, db: 1, offset: 0, bit: 3, type: BOOL` | `DB1.DBX0.3` |
| `area: DB, db: 1, offset: 4, type: REAL` | `DB1.DBD4` |
| `area: I, offset: 0, bit: 1, type: BOOL` | `I0.1` / `E0.1` |
| `area: Q, offset: 2, type: BYTE` | `QB2` / `AB2` |
| `area: M, offset: 10, type: INT` | `MW10` |

Tags in the same area/DB that sit close together are read in a single request.
gos7 splits a request that is larger than the negotiated PDU size into smaller
ones, so one poll needs only a few round trips.

### Exports

Exporters receive **changes only**: a value that changed (past its deadband),
plus quality transitions when the link drops or comes back. Each exporter runs
on its own queue, so a slow broker or database does not delay polling.

**Export settings window.** Everything below can also be set in the program:
the **Export** button at the top (its dot is green when every enabled exporter
works, red when one fails) or **File → Export → Export settings**. It has a tab each for
CSV, InfluxDB and MQTT, and applies changes without a restart (saved in
config.yaml). The helpers:

- **Status** of each exporter at the top: OK / CONNECTING / ERROR with the
  reason, and how many rows, points or messages were sent or are waiting.
- **Quick setup** buttons fill in typical values (InfluxDB 1/2/3 on this PC,
  on another computer, InfluxDB Cloud; Mosquitto on this PC, another broker,
  TLS, WebSocket, a public test broker after a warning). Typing just an IP
  becomes `http://IP:8086` (`:8181` for v3) or `tcp://IP:1883`.
- **Test connection**:
  - **InfluxDB**: reaches `/ping`, reads the server version, and offers
    *Use version N* if it differs. It checks the login (v1 `SHOW DATABASES`,
    v2 bucket list, v3 database list) and the organization. It lists the
    databases/buckets to pick from, and offers **Create** (`CREATE DATABASE`,
    a v2 bucket, a v3 database) when the configured one is missing.
  - **MQTT**: connects with a separate client ID (so the running exporter is
    not disconnected). It subscribes to `prefix/#`, publishes to `prefix/_test`
    and checks the message comes back, which also exposes ACL problems. It then
    lists the messages on the broker, including retained tag values.
  - **CSV**: checks that the file can be written (for example, that Excel is
    not locking it), and has *Open folder*.
- **Previews** of exactly what will be written, built from the live tags (line
  protocol, topics and payloads, CSV rows). There are also ready-to-copy
  queries (InfluxQL / Flux / SQL to match the version) and `mosquitto_sub`
  commands.

Each failure comes with a hint: nothing listening on that port, no answer
(firewall or IP), unknown host, TLS, a rejected token or password, or a missing
bucket. Export settings hold passwords and tokens, so they can only be viewed
and changed on the computer running PLC Monitor. If the exporters in the config
can't start (for example, the CSV file is locked), the program still starts and
the window shows why.

**Help.** The **Help** menu (or F1) opens a separate help window in English, or
Thai with the language button. It has illustrated guides for Modbus TCP, RS485
wiring and gateways, register addresses and word order, installing and
connecting InfluxDB 1/2/3 with Grafana queries, and Mosquitto and MQTT with
Node-RED. Each topic has a troubleshooting table.

**CSV**

```yaml
export:
  csv:
    enabled: true
    path: changes.csv
```

Columns: `timestamp,tag,address,type,old,new,quality`. The file is appended to,
and a header is written when it is new.

**MQTT**

```yaml
  mqtt:
    enabled: true
    broker: tcp://localhost:1883   # ssl://host:8883 for TLS
    client_id: plcmonitor
    username: ""
    password: ""
    topic_prefix: plcmonitor
    qos: 0
    retain: true
    payload: json                  # json | value
    tls_insecure: false            # true accepts self-signed certificates (tests only)
```

Broker URLs: `tcp://` (1883), `ssl://` / `tls://` / `mqtts://` (8883), `ws://`,
`wss://`. Each change is published to `<topic_prefix>/<tag name>`. With
`payload: json` (default) it looks like this:

```json
{"value":54.29,"quality":"good","address":"DB1.DBD4","type":"REAL","ts":"2026-10-28T08:12:01.123+07:00"}
```

With `payload: value` it is just the value as text: `54.29`, `true`, or `null`
while the value is bad. Simple HMIs, lamps and Node-RED gauges can use that
form directly.

`<topic_prefix>/status` is `online` while connected and `offline` otherwise. The
offline value is set as the last will, so it also appears if the process dies.
The MQTT client reconnects on its own. While it cannot connect, the Export
window says why: the port is closed, or the broker is reachable but refused the
login. Messages published while the broker is down are best effort and may be
lost.

**InfluxDB**

InfluxDB 1.x, 2.x and 3.x are supported. Set `version` and the settings that
version uses:

```yaml
  influxdb:                 # InfluxDB 1.x
    enabled: true
    version: 1
    url: http://localhost:8086
    database: plant
    retention_policy: ""    # optional
    username: ""            # optional (basic auth)
    password: ""
    measurement: plc
    flush_interval: 5s
```

```yaml
  influxdb:                 # InfluxDB 2.x (also the default if version is missing)
    enabled: true
    version: 2
    url: http://localhost:8086
    org: my-org
    bucket: plc
    token: "..."
```

```yaml
  influxdb:                 # InfluxDB 3 (Core, Enterprise, Cloud)
    enabled: true
    version: 3
    url: http://localhost:8181
    database: plant
    token: "..."            # empty if the server runs without authentication
```

| Version | Write request | Login | Health check at start |
|---|---|---|---|
| 1 | `POST /write?db=&rp=&precision=ms` | basic auth (username/password) | `GET /ping` |
| 2 | `POST /api/v2/write?org=&bucket=&precision=ms` | `Authorization: Token …` | `GET /health` |
| 3 | `POST /api/v3/write_lp?db=&precision=millisecond` | `Authorization: Bearer …` | `GET /health` |

At start the log says whether the server is reachable, so a wrong URL shows up
right away. Configs from before this option (url/org/bucket/token, no
`version`) keep working as version 2. Each point looks like
`plc,tag=Tank_Level,address=DB1.DBD4,type=REAL value=54.29 <ts>`. Every value is
stored as a float field (BOOL as 0/1), so all tags can share one measurement.
Since only changes are written, use "previous" fill (`fill(previous)`, or
`fill(usePrevious: true)` in Flux) when graphing. Failed writes are retried on
the next flush, with up to 8 MB kept in memory. A batch the server rejects as
bad data (HTTP 400/422, such as a field type conflict) is dropped and shown in
the Export window, not retried forever.

## Modbus: TCP/IP and RS485

Choose the protocol in **Network settings** (or `protocol: modbus` and
`modbus.mode` in the config):

| Network settings choice | `mode` | Use it for |
|---|---|---|
| Modbus TCP/IP (Ethernet) | `tcp` | Devices with an Ethernet port speaking Modbus TCP, and gateways that convert RS485 to Modbus TCP themselves. Settings: device address (`host` or `host:port`, port 502 by default) and unit ID. |
| Modbus RTU – RS485 / RS232 (COM port) | `rtu` | An RS485 bus on a USB-RS485 adapter or built-in serial port. Settings: COM port (picked from the ports Windows finds; ↻ looks again after plugging in an adapter), baud rate, data bits, parity, stop bits and default slave ID. |
| Modbus RTU – RS485 via Ethernet gateway | `rtuovertcp` | An RS485 bus behind an RS485-to-Ethernet converter in transparent / raw mode, which passes RTU frames through a TCP port unchanged. Settings: gateway address and port (often 502, 4001, 8899 or 23). The RS485 baud rate is set in the gateway itself. |

All Modbus modes also have the word order for 32/64-bit values (high word
first, the default, or low word first for "CDAB" devices), a timeout, and
*max gap*.

### Several slaves on one RS485 bus

An RS485 bus (or a gateway) usually has several devices on it. The network's
unit ID is the default. Each tag can set its own **Slave ID** in the tag form
(`unit:` in tag files) to read from another device on the same line. The
address column then shows it, e.g. `U2:HR0` is holding register 0 of slave 2.
Tags are grouped per slave, so each request goes to exactly one device.

How faults are handled on RS485 (`rtu` and `rtuovertcp`):

- **One slave stops answering** (powered off, wrong address, broken drop
  cable): only its tags turn bad, with "request timed out" and the slave
  number in the table. The other slaves keep updating, and the line is not
  reconnected. Each poll still waits for the timeout on the silent slave, so
  keep the timeout short (e.g. 200–500 ms at 9600 baud and above).
- **No slave answers at all:** the line itself is treated as down (adapter,
  cable, gateway, wrong baud rate or parity). The tool reports "no device
  answered" and reconnects, the same as a lost TCP connection.
- **Garbled replies** (bad CRC, short frame, e.g. from noise or missing
  termination) mark only the affected tags bad for that poll.
- **A gateway answering "target device failed to respond"** is handled like
  one silent slave.

Wiring tips for RS485: connect A/B (D+/D−) the same way on every device, plus
GND. Put 120 Ω termination at both ends of a long bus. Every slave needs a
different ID and the same baud rate, data bits, parity and stop bits.

Modbus tags use these areas. The **offset is the 0-based address sent on the
wire**, so holding register 40001 is `HR` offset 0, and 40011 is offset 10. The
tag form shows both forms, e.g. `HR10 (40011)`.

| Area | Meaning | Types |
|---|---|---|
| `HR` (4x) | holding registers | BOOL (bit 0–15 of a register), WORD/UINT, INT, DWORD/UDINT, DINT, REAL, LREAL |
| `IR` (3x) | input registers | same as HR |
| `COIL` (0x) | coils | BOOL |
| `DI` (1x) | discrete inputs | BOOL |

32-bit values use 2 registers and LREAL uses 4. Adjacent tags are read in one
request (up to 125 registers or 2000 coils). *Max gap* lets the tool also read
up to that many unused addresses to save requests. It defaults to 0 because
many devices reply "illegal data address" for unmapped registers.

If the device rejects a read like that, only the tags in that request are
marked bad, with the device's reason shown in the table; the connection stays
up. In TCP/IP mode, timeouts and lost connections reconnect as for ISO-TCP. On
RS485, see the fault rules above.

The simulator works for Modbus too: tick *Use the built-in simulator* with
Modbus selected.

## File menu: network and tag files

The **File** menu keeps network settings and tag lists in their own files, so
one tag list can be used with several PLCs, or several lines can be switched
quickly. It is grouped in submenus (they open on hover, or on a click):

| File menu | Holds |
|---|---|
| **Tag configuration ›** | New, Open… (Ctrl+O), Save, Save as…, and the recent tag files |
| **Network configuration ›** | New, Open…, Save, Save as…, Network settings…, and the recent network files |
| **Save all** (Ctrl+S) | saves whichever of the two files has changes |
| **Import ›** | PLC tag table (.xlsx / .xml / .sdf); data blocks from SCL source (.scl / .db / .udt); tag list from CSV (Excel); variable names for Scan PLC |
| **Export ›** | PLC tag table (.xlsx / .xml / .sdf); data blocks as SCL source (.scl); tag list as CSV (Excel); Export settings (CSV, InfluxDB, MQTT, …); Data view |
| **PLC ›** | Scan PLC for data blocks / tags; PLC tags (OPC UA); Topology; Windows network connections |
| **Add tag…** | the tag dialog |
| **Folders ›** | open the configuration folder or the trend history folder in Explorer |
| **Exit** (Alt+F4) | stop PLC Monitor |

**Tag list as CSV** has every field of every tag (name, area, db, offset,
bit, type, unit, deadband, description, node) and opens in Excel (UTF-8, so
Thai names show). **Tag list from CSV** reads it back, also after editing in
Excel (comma, semicolon or tab): tags with a name already in the list are
replaced, new ones are added, in one undo step. The recent files are kept
per computer (up to six of each kind); one that no longer exists is removed
when you pick it.

| Menu | Network configuration (`*.net.yaml`) | Tag configuration (`*.tags.yaml`) |
|---|---|---|
| **New** | default settings, no address and the simulator off (protocol kept), then opens Network settings | starts an empty tag list (add tags with **+ Add tag**) |
| **Open…** | load a network file and reconnect | load a tag list; polling switches at once |
| **Save** | write to the current file (Save as if there is none) | same |
| **Save as…** | write to a new file | same |

- **File → Exit** stops PLC Monitor: the desktop window closes, and the
  browser version's program stops and the tab says so. Exporters are flushed
  as on any normal shutdown. If a network or tag file has unsaved changes, it
  asks first; those changes are still kept in the workspace for next time.
  Exit only works on the computer running PLC Monitor, and it still works on a
  read-only dashboard, where the editing items are greyed out instead.
- **Ctrl+S** saves whichever of the two files is open and has changes.
- The menu bar shows the open file names. A **●** marks unsaved changes; New
  or Open asks before throwing them away.
- In the desktop app, Open and Save as use the normal Windows file dialogs. In
  the browser version you type the full path on the computer running
  PLC Monitor. The File menu only works on that computer; from other PCs it is
  greyed out and the API refuses it.
- A network file holds the protocol, simulator switch, ISO-TCP and Modbus settings,
  poll interval and reconnect timing. A tag file holds the tag list only.
- `config.yaml` stays the *workspace*. Every change is still saved there right
  away, together with the names of the open files, so after a restart you get
  exactly what you had, including unsaved changes (still marked ●).
- When the protocol changes (say, opening a Modbus network file while ISO-TCP tags
  are loaded), tags of the other protocol stay in the list but are **not
  polled**, and the table says why. Open or create a matching tag list next.
  The order doesn't matter.

Example files:

```yaml
# plant.net.yaml
protocol: modbus
simulate: false
modbus: {mode: tcp, address: 192.168.0.10, unit_id: 1, word_order: big, timeout: 2s}
poll_interval: 1s
```

```yaml
# line1.tags.yaml
tags:
  - {name: Pump_Speed, area: HR, offset: 10, type: REAL}
  - {name: Alarm, area: COIL, offset: 3, type: BOOL}
  - {name: Status_Bit12, area: HR, offset: 20, bit: 12, type: BOOL}
```

## Topology: finding PLCs and devices

**Topology** (header button) opens a window that scans a network and draws
what it finds as a tree: this PC → the network → each device.

1. Pick the network. It starts as **all**: every network of this PC that has
   a link (an adapter without a link is shown with *no link: cable?*). One
   network (`192.168.0.0/24`), one address, or several separated by commas
   work too; at most a /22 (1024 addresses) each. **This PC (127.0.0.1)**,
   ticked by default, also looks at this PC itself: virtual PLCs, simulators
   and gateways running here. With **all**, the same scan also searches
   every adapter with **PROFINET DCP**, which finds devices on the cable
   whatever their IP address and says for each whether this PC can reach it
   over IP (and which address to add if not). DCP needs Npcap on Windows (or
   the Linux agent); without it the tree offers *Download Npcap*.
2. Press **Scan**. Every address is tried on the ports industrial devices use:
   102 (ISO-TCP), 502 (Modbus TCP), 44818 (EtherNet/IP), 4840 (OPC UA), 80/443
   (web). 64 addresses are tried at the same time, with the timeout you set
   (400 ms default). A /24 takes a few seconds. **Stop** ends it early.
3. Each device found shows:
   - **ISO-TCP PLC:** the rack/slot that worked (0/1, 0/2, 0/0 or 0/3 are tried),
     CPU type, order number, **version**, name, serial number, **RUN/STOP** and
     the **PLC clock** with how far it is from this PC's (red when a minute or
     more off; "not reported" when the PLC doesn't give it). A
     CPU that won't identify itself over ISO-TCP is tested with a read of one
     memory byte: the card says whether reading works (PUT/GET on) or PUT/GET
     must be enabled; the identity then comes from its OPC UA server, if any.
   - **OPC UA server** (any device with port 4840, also a PLC with port 102):
     endpoint, security modes, guest access and – with guest access – model,
     order number, firmware, serial, and whether it publishes the PLC's tags
     (if not, it says to tick *Enable standard server interface*). Buttons
     **PLC tags (OPC UA)…** (the tags window on that endpoint) and **Monitor
     via OPC UA**.
   - **Modbus TCP:** whether it is a single device (answers on any slave ID)
     or a **gateway**. With *Find Modbus slave IDs behind gateways* ticked, it
     lists the IDs 1–247 that answer, i.e. the devices on the RS485 line
     behind the gateway.
   - Open ports, the host name (if DNS knows it), and a link to its web page.
   - **● monitored now** on the device PLC Monitor is connected to.
4. **Monitor this PLC (ISO-TCP)** / **Monitor via Modbus TCP** switches the network
   settings to that device (after asking), with the rack/slot or slave ID
   found.

**Slave IDs on the current connection:** when PLC Monitor is connected over
Modbus, the Topology window can scan slave IDs on that very connection. That
includes an **RS485 COM port**, which can't be opened a second time; the
probes run between two polls, so monitoring continues. A missing ID waits the
whole network timeout, so set it to about 300 ms before scanning 1–247.

The scan only connects and reads (the identity of an ISO-TCP PLC is read from
the CPU's system status list, and Modbus devices are asked for holding register
0). It never writes. It runs only from the computer PLC Monitor runs on. Still, some
plant networks don't allow scanning: ask first.

**Limits:**

- The IP scan finds devices that have an IP address and answer on these
  ports; firewalled devices are not found. PROFINET devices without an IP
  address are found by **Find PROFINET devices** (below).
- The modules inside a PLC rack (I/O cards, CPs) are not listed: the ISO-TCP
  library doesn't expose that part of the protocol. CPUs and other
  IP-addressed stations (ET 200SP CPUs, HMIs, drives with Modbus TCP) do show
  up as their own devices.

### PROFINET devices: set IP address and name of station

The **PROFINET devices** panel in the Topology window works like *Accessible
devices* / *Assign IP address* in the PLC engineering software. It is hidden when the window
opens. The **▸ PROFINET devices** button in the Topology bar shows or hides
it, and nothing is sent to the agent or looked up until it is first shown. It uses PROFINET DCP, which runs
directly on Ethernet, below IP. Windows lets programs send such frames only
through a driver, so there are two ways, chosen with **Through**:

- **Linux agent (default).** This needs nothing on the Windows PC. A Linux
  computer on the PROFINET network sends the frames: an industrial PC, a
  Raspberry Pi, or a laptop with Linux. Linux can do this itself with a raw
  socket, so no extra software is needed there either. The Windows Topology
  window passes the requests to it and shows the results as usual.
- **This PC.** On Windows this needs the free [Npcap](https://npcap.com)
  driver, installed with *WinPcap API-compatible mode*. **Download Npcap**
  opens its web page; after installing, **Check again** enables it without
  restarting. On Linux, *This PC* works directly and is the default there.

**Setting up the Linux agent** (once):

1. Copy `plcmonitor-linux-amd64` (PC) or `plcmonitor-linux-arm64` (Raspberry Pi
   4/5) from the PLC Monitor folder to the Linux computer.
2. Run `chmod +x plcmonitor-linux-* && ./plcmonitor-linux-amd64 install-agent`.
   It asks for the sudo password by itself, then:
   - copies itself to `/usr/local/bin/plcmonitor`;
   - creates a random token in `/etc/plcmonitor/agent.token` (readable by root
     only);
   - installs the systemd service `plcmonitor-dcp`. The service starts at boot
     with **only the `CAP_NET_RAW` capability** (raw network access and none
     of root's other powers), with `NoNewPrivileges` and a read-only system.
   - It prints the **address** (`http://<ip>:8095`) and the **token**.
3. In the Topology window choose *Through: Linux agent*, enter the address and
   token, and press **Connect**. The adapter list then shows the Linux
   computer's adapters (eth0, …); pick the one wired to the PROFINET devices.

To start the agent by hand instead, run `./plcmonitor-linux-amd64 -dcp-agent
:8095 -token <token>`. If it lacks the permission it restarts itself through
sudo. Every request to the agent needs the token, also from the Linux computer
itself, so other users there cannot reconfigure devices. Allow TCP 8095 in its
firewall (`sudo ufw allow 8095/tcp`). To remove the service, run `sudo
plcmonitor uninstall-agent`. The agent's web page `http://<ip>:8095/` shows
whether it is ready.

The token goes over the network unencrypted (plain HTTP), like most plant
protocols. Use the agent only on the plant network, not across the internet.

1. Choose the **adapter** the devices are wired to, and press **Find
   PROFINET devices**. Every device on that Ethernet segment answers, also
   ones without an IP address. The tree shows the name of station, type (e.g.
   a 1200-series CPU, ET200SP), MAC, IP address / mask / gateway, vendor and role. It
   also flags devices with no name and IP address conflicts.
2. **Flash LED** makes the device's LEDs flash for a few seconds, so you can
   find it in the cabinet.
3. **Configure…** sets the **IP address, subnet mask and gateway** and/or the
   **name of station**:
   - **Keep after power off** stores it permanently; untick it to keep it only
     until the next power off.
   - Names follow the PROFINET rules (lower case a–z, 0–9, `-`, `.`); spaces
     and capitals are corrected as you type.
   - The IP address is checked against the mask (no network or broadcast
     address, gateway in the same subnet).
   - Apply works only after you tick *I am allowed to change this device*,
     and asks again with a before → after summary.
   - The device's own answer is shown. For example, a CPU that takes its IP
     address from its engineering project refuses ("set not possible for local
     reasons"), and then it must be changed in the PLC engineering software.
   - Afterwards the list is read again, and every change goes into
     plcmonitor.log.

Finding devices, flashing and configuring work only on the computer running PLC
Monitor, and configuring not on a read-only dashboard. **Changing the IP
address or name of a device in a running plant breaks every connection to it**
(PLC program, HMI, drives): do it only when that is safe.

### Exporting the topology

The **▸ Export view** button in the Topology bar shows the export buttons; they
are hidden when the window opens. **PNG / JPG / PDF** saves the topology as a
picture or a one-page
PDF. It is drawn from the scan results, not taken as a screenshot, so it is
complete however the window is scrolled. It includes this PC, the scanned
network with every device's details, and the PROFINET devices. The desktop app
asks where to save it with the Windows *Save as* dialog; a browser downloads
it.

## Dashboard and API

The live table refreshes every second. Changed rows flash, bad-quality values
are red, and the header shows link state, the last error, poll timing and the
number of reconnect attempts.

### Always on top

In the desktop app, the **📌 Always on top** button at the top right (blue while
it is on), **View → Always on top** or **Ctrl+T** keeps PLC Monitor above other
windows, which is handy beside the PLC engineering software or an HMI. The Network settings, Topology, Export and Help windows stay
on top with it, so they don't disappear behind the main window. The setting is
saved in config.yaml (`web.always_on_top`). It is not available in a browser.

### First run and the sample config

On its first run the program writes `config.yaml` from the built-in
`config.sample.yaml`, which has comments and 15 example tags, with the
simulator off and no addresses filled in, so nothing on your network is
contacted until you enter an address. Editing your own `config.yaml` (or
File → New) never changes what a new installation starts with.

### Themes

**View → Theme…** opens a picker with 25 colour themes. Click a theme to
preview it, then press OK; Esc or Cancel goes back to the previous one. The
View menu also has a quick list of six themes. The Export and Help windows have
a *Theme* button too.

- **Automatic:** Follow Windows (light / dark).
- **Light:** Light, Sky, Mint, Lavender, Rose, Paper, Solarized Light, HMI Gray
  (a calm grey in the style of high-performance HMIs), High Contrast Light.
- **Dark:** Dark, Midnight, Graphite, Deep Ocean, Forest, Coffee, Sunset, Nord,
  Dracula, Gruvbox, Solarized Dark, Neon, Green Terminal and Amber Terminal
  (monospace font), High Contrast Dark.

The theme applies at once to every open PLC Monitor window: main, Network
settings, Topology, Export and Help, charts included. On the computer running
PLC Monitor it is saved in config.yaml as `web.theme`, so the desktop app keeps
it after a restart. Browsers on other computers remember their own choice. The
themes are defined in `web/themes.js`; a new one needs only its nine base
colours there.

### Charts

Every row has a **Chart** button. It opens a live chart card above the table.
Each card has:

- ranges 1m / 5m / 15m / 1h, and with trend history 8h / 24h / 7d / 30d,
- ◀ ▶ to move back and forward by half the range, a date/time box to show the
  period ending at any moment, and **Live** to follow the current values again,
- **CSV** to download the shown period at full resolution,
- min / max / average for the shown range (BOOL: % of samples that were TRUE),
- a hover read-out of the exact time and value,
- a × to close it.

BOOL tags are drawn as steps with the TRUE periods shaded. Gaps in the line are
times when there was no good value: the link was down, a read failed, or
PLC Monitor wasn't running. You can open as many charts as you like.

**Several tags in one chart.** **+ Chart** (above the table) or **Window → New
chart…** opens a picker: tick up to 16 tags, give the chart a name, and choose
the scale and where it shows. An existing chart can be changed and combined:

- **+ Add tag…** in its legend adds a tag. The same list also has **Merge
  another chart into this one**.
- **Drag a chart by its title onto another chart** to combine the two.
- **Drag a tag row from the table onto a chart** to add that tag. While you
  drag, the charts are outlined and a box appears below them: drop the tag
  there for a new chart with just that tag. In a tag window that box opens the
  chart in its own window.
- Click a tag in the legend to hide or show its line; its **×** takes it off
  the chart.
- **Scale: shared / per tag.** Shared puts all numbers on one axis. Per tag
  scales each line to its own min–max (0–100%), for tags with very different
  ranges such as a speed of −1000 and a level of 0–100. The hover read-out
  always shows the real values of every tag.
- BOOL tags in a chart with numbers each get an on/off lane at the bottom.
- Double-click the title to rename the chart.
- **CSV** downloads all its tags side by side (one column per tag).

**In the main window or a window of its own.** Each chart has **⇱** to move it
into its own window; in that window **⇲** puts it back. The main window then
shows a small bar for it with *Bring to front* / *Open window* / *Show here*.
**Window → Open new charts** sets where new charts appear: in the main window
or in their own window.

**Tag windows.** **Window → New tag window…** creates an extra window that
shows only the tags you pick, for example one per production line. Each has
its own name, and the Window menu lists them to open again. In a tag window:

- **Edit tags…** changes the tags it shows.
- **Rename…** and **Delete window** manage it; deleting the window does not
  delete the tags.
- **+ Add tag** adds a new tag, which also goes into this window.
- **Chart** buttons open the chart in its own window.

The layout is saved in config.yaml under `layout:` and comes back after a
restart: the charts (tags, name, scale, range, which have their own window) and
the tag windows. Charts that had their own window open it again at start. A
browser on another computer keeps its own layout. Charts and tag windows follow
renamed tags, and a deleted tag leaves its charts.

The last 3600 polls of every tag are also kept in memory for the live part of
the chart (one hour at a 1 s poll interval).

### Trend history files

PLC Monitor records every tag to disk, so charts can show the past and the data
survives restarts:

```
history/
  2026-11-18/
    Tank_Level.csv
    Motor1_Speed.csv
  2026-11-19/
    ...
```

- **One CSV file per tag per day** (local date), with the columns
  `unix_ms,time,<tag name>`, e.g. `1795050618426,2026-11-19 08:10:18.426,72.45`.
  The files open directly in Excel.
- An **empty value** marks a gap: no good value, or the program wasn't running.
- BOOL is stored as 0/1.
- A tag is written at most once per `interval`, and only if its value changed,
  or `heartbeat` has passed. A steady value therefore costs about one line a
  minute, and a value changing every second costs 86,400 lines (about 3 MB) a
  day.
- Tag names with characters not allowed in file names get a safe name plus a
  short code (e.g. `Tank_Level~1a2b3c4d.csv`). Renaming a tag starts a new
  file; the old one stays under the old name.
- The `history` folder sits next to `config.yaml` (desktop app) or in the
  working folder (console). **File → Folders → Trend history folder** opens it in
  Explorer.
- Day folders older than `keep_days` are deleted automatically (checked at
  start and every 10 minutes).
- Long chart ranges are reduced to about one point per pixel by keeping each
  slot's minimum and maximum, so short spikes still show. The CSV download is
  always full resolution.
- Writing is buffered and flushed every second. If the disk can't keep up,
  samples are dropped and the log says how many; polling is never slowed down.

Settings (all optional):

```yaml
history:
  disabled: false   # true turns the files off (charts then go up to 1 h, from memory)
  path: history     # folder; relative to the working folder
  interval: 1s      # at most one line per tag per interval
  heartbeat: 1m     # write an unchanged value at least this often
  keep_days: 30     # delete older days; -1 keeps everything
```

### Writing values and signal generators

Every writable tag has a **Write** button. It opens a window with two parts.

**Write once:** BOOL tags get *Write TRUE*, *Write FALSE* and *Toggle*. Number
tags get a value box. Press Enter or *Write*. The value is checked against the
type (e.g. INT −32768..32767, BYTE 0..255; whole numbers also accept `0x1F`),
and written between two polls, so it never collides with a read.

**Signal generator:** keeps writing a signal until you stop it.

| Tag type | Signal | Settings |
|---|---|---|
| BOOL | **Loop delay**: TRUE for the ON time, FALSE for the OFF time, repeating | ON time, OFF time (ms) |
| BOOL | **Loop tick**: a short TRUE pulse every period (clock / heartbeat) | pulse width, period (ms) |
| BYTE/SINT, WORD/INT, DWORD/DINT, REAL/LREAL | **Sine**, **Square**, **Sawtooth**, **Random** | min, max, period, write every (ms) |

- Square is max for the first half of the period and min for the second.
  Sawtooth ramps min→max, then jumps back. Random picks a new value at every
  write.
- Integer types are rounded and kept inside min..max and the type's range.
- BOOL signals are written only when they change; numeric signals at every
  write interval.
- The fastest allowed rate is one write per 50 ms, to keep the PLC or bus
  usable.
- A running generator shows a **GEN** badge on the value, its settings in the
  Description column, and its last write error if any. **Stop all generators
  (N)** above the table stops every one.

Safety rules:

- Against a real PLC or device, every write and generator start asks for
  confirmation, naming the target. With the simulator on it doesn't.
- Generators are not saved. They stop when you exit, when the tag is deleted
  or its address/type changes, and **when the network settings change**, so
  they never carry over to a different PLC.
- Modbus input registers (IR) and discrete inputs (DI) are read-only and have
  no Write button.
- Writing is allowed only on the computer running PLC Monitor (the desktop app,
  or a browser on that PC). Dashboards opened from other PCs show no Write
  buttons unless you set `web.allow_remote_write: true`. `web.read_only: true`
  turns writing off everywhere.

How values are written:

- **ISO-TCP BOOL:** a single-bit write, so the PLC changes only that bit, not the
  other seven in the byte.
- **ISO-TCP, other types:** the bytes of the value (DB, I, Q or M).
- **Modbus coil:** Write Single Coil.
- **Holding registers:** Write Single / Multiple Registers, using the
  configured word order.
- **Bit of a holding register:** the register is read, the bit changed and
  the register written back. Modbus has no bit write for registers, so a
  change another master makes to the same register in between would be
  overwritten.
- **Simulator:** a written tag stops being animated and keeps your value, or
  shows your generator's signal.

### More export targets

Besides CSV, InfluxDB and MQTT, the Export settings window has a tab for
each of these. Each has **Test connection** (or **Check** for the servers),
and its status shows in the overview at the top and in Network settings.

| Target | How PLC Monitor talks to it | What arrives |
|---|---|---|
| **VictoriaMetrics** | InfluxDB line protocol, `POST <url>/write` (port 8428) | series `plc_value{tag,address,type}` |
| **QuestDB** | InfluxDB line protocol over HTTP (ILP, port 9000) | table `plc_values` (tag, address, type, value, timestamp), created by QuestDB |
| **TimescaleDB** | PostgreSQL connection string, batched `COPY` | table `plc_values (time, tag, address, type, value)`, created on request, a hypertable when the extension is installed; plain PostgreSQL works too |
| **ClickHouse** | HTTP interface (port 8123), `INSERT … FORMAT JSONEachRow` | table `plc_values`, MergeTree ordered by (tag, time), created on request |
| **AMQP** (RabbitMQ …) | AMQP 0-9-1, exchange `amq.topic` | routing key `plcmonitor.<tag>`, JSON or value payload as for MQTT |
| **CoAP** | UDP, POST or PUT, confirmable or not | `coap://host:5683/<path>/<tag>`, JSON or text payload |
| **Prometheus** | PLC Monitor serves `http://<this PC>:9273/metrics` | `plcmonitor_value`, `plcmonitor_good`, `plcmonitor_last_change_seconds` with labels tag, address, type |
| **gRPC** | PLC Monitor serves `plcmonitor.v1.Monitor` on port 50051, with reflection | `List` and `Subscribe` (stream); the `.proto` is linked in the window and below |
| **OPC UA server** | PLC Monitor serves `opc.tcp://<this PC>:4840` (security None, anonymous, read-only) | `Objects/PLCMonitor/<tag>`, node ID `ns=1;s=<tag>`, typed, Bad status when the read fails |

The databases receive every good value, collected and written every few
seconds (*Send every*); when the server is down they are kept (up to 200 000
values) and sent later. The servers keep the latest value of every tag and
answer whoever connects. They have no login or encryption: use them on a
trusted network, and allow the port in the Windows firewall.

```yaml
export:
  victoriametrics: { enabled: true, url: http://192.168.1.20:8428, measurement: plc }
  questdb:         { enabled: true, url: http://192.168.1.20:9000, measurement: plc_values }
  timescaledb:     { enabled: true, dsn: "postgres://plc:secret@192.168.1.20:5432/plc?sslmode=disable", table: plc_values, create_table: true }
  clickhouse:      { enabled: true, url: http://192.168.1.20:8123, database: default, table: plc_values, create_table: true }
  amqp:            { enabled: true, url: amqp://plc:secret@192.168.1.10:5672/, exchange: amq.topic, routing_prefix: plcmonitor, payload: json }
  coap:            { enabled: true, url: coap://192.168.1.30:5683/plcmonitor, method: post, payload: json }
  prometheus:      { enabled: true, listen: ":9273" }
  grpc:            { enabled: true, listen: ":50051" }
  opcua_server:    { enabled: true, listen: ":4840" }
```

The gRPC API (`GET /api/export/grpc.proto` gives the same text):

```proto
syntax = "proto3";
package plcmonitor.v1;
service Monitor {
  rpc List(ListRequest) returns (ListResponse);
  rpc Subscribe(SubscribeRequest) returns (stream TagValue);
}
message ListRequest {}
message ListResponse { repeated TagValue tags = 1; }
message SubscribeRequest { repeated string tags = 1; bool skip_current = 2; }
message TagValue { string name = 1; string address = 2; string type = 3; double value = 4;
                   string text = 5; bool good = 6; int64 time_unix_ms = 7; }
```

### Data view: the tags in MQTT and InfluxDB

**File → Export → Data view** (also in the ⚙ menu and the Export settings window)
shows what the exporters make of every tag, live:

- **MQTT tab:** the broker, topic pattern and payload format, then per tag
  the topic and the payload PLC Monitor publishes, next to what the broker
  holds now (✔ when the value agrees, *retained*, how many messages and how
  long ago). A separate client with its own ID subscribes to `<prefix>/#`
  while the window is open, so other topics such as `<prefix>/status` show
  too.
- **InfluxDB tab:** server, version, database or bucket and measurement, then
  per tag the line written (line protocol) next to the last point read back
  from the database (last 30 days), refreshed every 5 seconds. The query is
  shown (InfluxQL for 1.x, Flux for 2.x, SQL for 3) with a Copy button, as a
  starting point for Grafana.

It uses the saved Export settings, works only on the computer running PLC
Monitor, and shows what *would* be sent when an exporter is off.

For developers, a test MQTT broker and an InfluxDB 2.x stand-in:

```powershell
$env:PLC_EXPORT_SERVE = "$env:TEMP\plcexport.txt"; go test -run TestExportServe -timeout 0
```

(the file then holds their addresses).

### Connect / Disconnect

The **⏻ Disconnect** button in the top bar closes the connection to the
PLC/device at once.

- **While offline:** no reads, no reconnect attempts and no writes. Running
  signal generators are stopped.
- **What you see:**
  - a grey **OFFLINE** label with "disconnected by you" next to the target;
  - the last values in red, since they are no longer live;
  - the desktop window title ends in "(offline)".
- **▶ Connect** connects again immediately. That is not counted as a
  reconnect attempt.
- Settings stay unchanged, and the connection comes up on the next start of
  the program.
- Not available on a read-only dashboard.

### Status window

The main window shows only the link state and target. The details are in the
**Status** window: **Status → Open status window**. It is a window of its own
that can sit beside the main one, follows Always on top, and closes with the
main window.

In the **Status** menu, tick the items to show. The menu stays open, so you
can tick several. **Choose items…** in the window does the same, and *Show
all* / *Default selection* are in the menu.

| Item | Shows |
|---|---|
| Connection | CONNECTED / DISCONNECTED, protocol or simulator |
| Target | PLC / device address, rack/slot or slave |
| Poll interval | how often all tags are read |
| Read time | the last poll's read time, with min / avg / max and a small trend line over the last 120 polls |
| Polls per second | actual poll rate and total polls |
| Read blocks | requests per poll |
| Reconnect attempts | since the last connect |
| Last error | the last connection or read error |
| Tags good / bad | how many tags read fine, fail, or are not polled |
| Value changes per second | how busy the values are |
| Connected for | time since the link came up |
| Last poll | when the last poll finished |
| Signal generators | how many are running, and on which tags |
| Exporters | CSV / MQTT / InfluxDB state and errors |
| Network and tag files | the open files and unsaved changes |
| Mode | simulator or live device, read-only or not |

The default selection is the first eight items except polls per second. The
choice is saved in config.yaml with the window layout.

### Working with the tag table

The table shows live values without colour animation.

**Columns.** Click any column heading, or **⚙ Columns** at the right end of
the header, to choose what is shown:

- *Address*, *Type*, *Value*, *Modify*, *Description*, and *Buttons*
  (Chart · Edit · Delete). *Tag* always stays.
- **Show all** brings everything back.
- The choice applies to the main window and the tag windows, and is saved in
  config.yaml with the window layout.
- With the Buttons column hidden, Chart, Edit and Delete are still in the
  right-click menu.
- Values are written in the **Modify** column. The Write window, for a
  single write or a **signal generator**, opens with right-click → *Write… /
  signal generator*.

**Modify column** (like *Modify value* in a watch table of the PLC
engineering software). Every
writable tag has a text box for preparing a value:

- **Enter** or the row's **▶** writes it to the PLC/device.
- **⚡ Modify selected** (or **Ctrl+Enter**, or right-click → *Modify now*)
  writes the prepared values of the selected rows.
- **⚡ Modify all** writes every prepared value; the button shows how many.
- On a real device one confirmation lists every tag ← value before anything
  is written. On the simulator nothing is asked.
- **The result shows on the box:** green when written (fades after 3 s), red
  with the reason as the tooltip when refused (e.g. *"abc" is not a whole
  number*).
- The prepared values stay until you change them, so the same values can be
  written again later, like a watch table. They are not saved when the
  program closes.
- Values are typed as in the Write window: `TRUE`/`FALSE`/`1`/`0` for BOOL,
  numbers, and `0x…` / `0b…` for hex and binary.
- **↑/↓** move between the boxes and **Esc** leaves them. While you type, the
  rest of the row keeps updating.
- Tags that can't be written (inputs on Modbus, read-only dashboards, other
  computers without `allow_remote_write`) show "—".
- Hide the column with ⚙ Columns if you don't need it.

Rows can be selected and edited like a list in Excel or Explorer:

| Action | How |
|---|---|
| Select one row | click it (not on a button) |
| Add / remove single rows | **Ctrl+click** |
| Select a range | **Shift+click** (from the last clicked row); Shift+↑/↓ |
| Select all / none | **Ctrl+A** / **Esc**; ↑/↓ move the selection |
| Copy | **Ctrl+C**, or right-click → Copy |
| Cut | **Ctrl+X** |
| Paste | **Ctrl+V**: the tags go after the last selected row (or at the end) |
| Delete | **Del**, the row's Delete button, or right-click → Delete |
| Insert | **Ins**: a new tag after the selected one |
| Add | **+ Add tag**: a new tag at the end |
| Undo / Redo | **Ctrl+Z** / **Ctrl+Y** (or Ctrl+Shift+Z), or the ↶ ↷ buttons |
| Edit | double-click a row |

Right-click a row for the same commands. That menu also has *Chart of these
tags*, which makes one chart of up to 16 selected tags.

**Copy and paste** use tab-separated text with a header row (`name area db
offset bit type unit deadband description`):

- Copied tags paste into another tag window, a second PLC Monitor, or
  **Excel**.
- A list made in Excel pastes back as tags. Only the name, area and type
  columns are required; the columns may be in any order, and *DB number*,
  *Byte offset*, *Slave ID* and *Comment* are understood too.
- A pasted tag whose name already exists gets `_copy`.

**Address helper.** The tag dialog starts with an **Address** box. Type the
address the way the PLC engineering software or a device manual writes it, and the area, DB,
offset, bit and type are filled in:

| Protocol | You can type |
|---|---|
| ISO-TCP | `DB1.DBX0.3`, `DB1.DBB1`, `DB1.DBW2`, `DB1.DBD4`, `MW10`, `MD20`, `MB5`, `M10.0`, `IB2`, `I0.1`, `QW4`, `%Q2.0`; the German mnemonics `E0.1` and `AW4`; `PIW256` (read from the input image) |
| Modbus | `40001` / `400001` (= HR 0), `30010` (= IR 9), `00005` (= coil 4), `10001` (= DI 0), `HR100`, `IR7`, `C5`, `DI3`, `HR3.4` (bit 4 of register 3), `U2:40001` (slave 2) |

- **Type.** A `W` or `D` address keeps a type of that size that is already
  chosen (WORD/UINT/INT, or DINT/DWORD/UDINT/REAL); otherwise INT, REAL or
  BYTE is used.
- **Mistakes** are explained under the box: a missing bit (`M10` → `M10.0`?),
  a bit on a word, bit 8 or more, `40000`, or an absolute address on a Modbus
  network.
- **Both ways.** Changing the fields below updates the address box, and on
  leaving the box the address is shown in its standard form.
- **Next free** gives the first unused address after the existing tags in the
  same area, DB and slave, for the chosen type. ISO-TCP words and double words
  start on an even byte.
- **Overlaps** with existing tags are shown as a warning, e.g. *overlaps
  Tank_Level (DB1.DBD4)*. Saving is still allowed, because reading bits of a
  status word is a common reason to overlap.
- **The name** of a new tag follows the address (`MW12`) until you type a name
  yourself. When adding, the cursor starts in the address box.

**Insert and Add** suggest the next address of the same kind: after
`DB1.DBW2` (INT) comes `DB1.DBW4`, after `M10.0` comes `M10.1`, and after
`M0.7` comes `M1.0`. They also suggest a name: `M0.2` → `M0.3` when tags are
named after their address, and `Pump_1` → `Pump_2`.

**Undo/redo** covers every change made in this window: add, insert, edit,
delete, cut and paste, up to 100 steps. If the tag list was changed elsewhere
in the meantime (another window, or File → Open), it asks before undoing.
Deleting no longer asks for confirmation, because Ctrl+Z brings the tags
back.

### Scan PLC: data blocks to tags

**Tools → Scan PLC for data blocks / tags…** opens a window that finds what
the PLC lets PLC Monitor read, shows the bytes, and turns the ones you pick
into tags. It works on ISO-TCP connections (and the simulator), and only reads:
monitoring goes on while it runs.

1. **Scan** tries DB numbers *from*–*to* (default 1–1000) and lists the data
   blocks that answer, with their size. **M, I and Q too** also measures the
   marker, input and output areas. **Ask the CPU for its DB list** also uses
   the block list of 300/400-series CPUs, which finds DBs outside the range.
   1200/1500-series CPUs do not have that list, so DB numbers are tried one by one,
   up to 18 in one request; 1000 numbers take a few seconds.
2. Click a found area (or **Open by number**) to see its bytes. **View as**
   shows them as BOOL (8 bits per byte), BYTE, INT, WORD, DINT, REAL, LREAL,
   and so on, big-endian as in the PLC. **Bytes** chooses the range (◀ ▶ page
   through larger areas), **Hide zeros** skips empty rows, **Live** reads again
   every second. Addresses that already are a tag show the tag's name; in the
   BOOL view their bits have a dashed border.
3. Tick rows, or click single bits in the BOOL view. You can pick from several
   views and areas; the picks are listed at the bottom. **Pick all shown** takes
   every visible row.
4. **Add as tags** appends them to the tag list, named after their address
   (`DB1.DBD4`, `M10.3`, …), which you can rename in the main window (Undo
   there does not cover this step). Addresses that already are a tag are
   skipped.

**Variable names.** The PLC only sends bytes, so names come from files you
export from the PLC engineering software. Import them with **📄 Variable names…**:

| What | How to export | File |
|---|---|---|
| Data blocks, also instance DBs of function blocks | Select the DBs (and their PLC data types and function blocks) in the project tree → right-click → **Generate source from blocks** → *Including all dependent blocks*. Older (classic) engineering software: *Generate source*. | `.db`, `.udt`, `.scl`, `.awl` |
| Inputs, outputs, markers | Open the PLC tag table → **Export** | `.xlsx`, `.xml` (the tag table's own XML export, or an Openness XML export) or `.sdf`; also `.csv` with Name, Data Type, Logical Address, Comment |

To use a PLC tag table as **tags** rather than only as names: File → Import
→ **PLC tag table (.xlsx / .xml / .sdf)…**, or pick the file in
File → Tag configuration → **Open…** (its file types include PLC tag
tables). Every I, Q and M tag of the table is added with its name, type and comment. A tag whose name
is already in the list is updated from the file instead of being added
again. Rows without an address and types such as String are skipped. The
whole import is one undo step.

The other way: File → Export → **PLC tag table (.xlsx)…**
**PLC tag table (.xml)…** or **PLC tag table (.sdf)…** writes the tag list as a PLC tag table. The .xlsx
has the sheet *PLC Tags* with the columns of the engineering software's own tag
table export (Name, Path, Data Type, Logical Address, Comment, Hmi Visible, Hmi
Accessible, Hmi Writeable), path *Default tag table*. In the PLC engineering
software: open the PLC tags →
**Import** (the button in the tag table toolbar) and choose the file. Only I,
Q and M tags belong in a tag table, so DB, Modbus, OPC UA and LReal tags are
left out (the message says how many).

**SCL sources.** Data blocks work the same way, in both directions:

- **File → Import → Data blocks from SCL source (.scl / .db / .udt)…** (or
  the file in File → Tag configuration → **Open…**) reads a source made with
  *Generate source from blocks* – data blocks, PLC data types, and function
  blocks for instance DBs. A dialog lists each data block with its size and
  DB number. A source has no numbers: they come from Scan PLC's imported
  names, or by size from the PLC – **Find DB numbers on the PLC** scans it
  (only reads) and fills in every block for which exactly one data block of
  that size exists; the *In the PLC* column shows ✓ when the sizes agree and
  ⚠ when they don't (another version of the block, or the wrong number).
  **Add as tags** adds the variables of the ticked blocks as tags named
  `Block.Variable` (e.g. `FB_FluffSilo_DB.Blower01.Status.Fault`); tags with
  the same name are updated, and one Ctrl+Z undoes it.
- **File → Export → Data blocks as SCL source (.scl)…** writes the tags in
  data blocks as SCL data blocks with standard access (one per DB number),
  for *External source files → Generate blocks from source*. Members are
  placed so every tag keeps its address; `Spare_…` members fill the gaps,
  and each block is laid out again and compared before it is written. The
  block is named after the tags' common first part (`DB_HMI.…`), an imported
  name, or `DB<n>`; give it its number in the block properties. Names lose
  characters a member name can't have (`Dev[0].Run` → `Dev_0_Run`). Tags
  outside data blocks, overlapping tags and 16-bit or longer values at an
  odd byte are left out (the message says how many). The blocks are flat:
  structures and arrays of the original become single members at the same
  places.

- From a DB source, PLC Monitor works out the offset of every variable with
  the layout rules of standard (non-optimized) access: structures, arrays
  (also of Bool and multi-dimensional), PLC data types, strings.
- A source generated by the engineering software has the DB's name but not
  its number. Type the **DB number**
  in the dialog. It is filled in by itself when the name is like `DB10`, or
  when exactly one scanned DB has the same size. The dialog shows whether the
  size matches the PLC (⚠ means the PLC has another version of the block, or
  the number is wrong).
- With names, a block opens in the **Names** view: name, address, data type,
  live value and comment of each variable. The other views show the names in
  an extra column.
- Picked variables become tags named `DBName.Path` (e.g.
  `Line_Data.Motor1.Speed`) or the tag table name, with the comment as the
  description.
- The files are kept in `plc-symbols.json` next to the config, so the names
  are there next time. Import a newer export again to update them.
- **Instance DBs of function blocks** are laid out from the FB's source:
  inputs, outputs, in/outs and statics in that order, each section from an
  even byte; multi-instances of other FBs; the built-in timers (TON, TOF, TP,
  TONR and their _TIME forms: 16 bytes, PT and ET readable), counters (CTU,
  CTD, CTUD) and edge detectors (R_TRIG, F_TRIG, whose three bits go into the
  free bits of the section's last Bool byte). Checked against a 1200-series
  CPU: a 486-byte instance DB with five FB multi-instances, timers and edge
  detectors matched to the byte. Import the FB's source with it (Generate
  source from blocks → *Including all dependent blocks*).
- Not supported: optimized blocks (which PUT/GET can't read anyway). The
  dialog says so for such blocks.
  Strings, DTL and other types that aren't tag types are shown but can't be
  picked.

Limits of PUT/GET, which this uses:

- **Symbol names and data types are not in the PLC's answers**, only bytes.
  Without imported names you choose the type with *View as*, and names come
  from the address.
- A data block is only found when it is **not optimized** ("Optimized block
  access" switched off in the block's properties). Optimized DBs, and DBs of
  a PLC without "Permit access with PUT/GET", look the same as missing ones.
- The size is that of the readable part: the highest byte that can be read,
  plus one.

### PLC tags over OPC UA: names straight from the PLC

PUT/GET only carries bytes, so the PLC never sends the names you gave your
tags. 1500-series CPUs and 1200-series CPUs (firmware 4.4+) have an **OPC UA
server** that
publishes every variable by its symbolic name, including optimized data
blocks. **Tools → PLC tags (OPC UA)…** uses it:

- **Read tag list from PLC** browses the server (endpoint
  `opc.tcp://<PLC IP>:4840`, security None / Sign / Sign & encrypt, anonymous
  or user name). It lists each variable's name (`Line_Data.Motor.Speed`,
  `Start_PB`), area (I, Q, M, DB), data type, comment, live value and node ID,
  with a filter and area chips.
- **Add as tags** adds the ticked variables with the PLC's names and comments.
  *Read as*:
  - **OPC UA (by name)**, when Network → Protocol is **OPC UA**;
  - **Absolute address**, for an ISO-TCP connection, when the address is known from the
    names imported in Scan PLC (OPC UA gives no absolute addresses).
- **Save as tag file…** writes the ticked (or shown) variables as a
  `.tags.yaml` file for File → Tag configuration → Open.
- **Export CSV…** saves the list for Excel.
- **Compare / update…** checks every tag against the PLC: match, differs
  (name, type or comment changed), or not in the PLC. Tick rows and **Apply**
  to update names, types and descriptions, or remove tags that are gone.

**Monitoring over OPC UA:** Network settings → Protocol **OPC UA**. Tags then
have `area: UA` and a `node:` such as `ns=3;s="Line_Data"."Speed"`. All tags
are read with one OPC UA Read per 100 nodes; a missing node marks only its own
tag bad. Writes use the matching OPC UA type (INT → Int16, REAL → Float, …).

```yaml
protocol: opcua
opcua:
  endpoint: opc.tcp://192.168.0.1:4840
  security: none          # none, sign or signandencrypt
  policy: Basic256Sha256  # with security
  username: ""            # empty: anonymous
  timeout: 5s
tags:
  - name: Line_Data.Speed
    area: UA
    node: ns=3;s="Line_Data"."Speed"
    type: INT
```

In the PLC engineering software: CPU properties → OPC UA → Server → **Activate
OPC UA server**, choose the OPC UA runtime license, and allow the security you
use (for a first test *No security* and *Guest*). With Sign or Sign & encrypt,
the PLC must trust PLC Monitor's certificate (`opcua-client-cert.pem`, created next to the
config on first use), or accept client certificates automatically. Help →
*Tag names from the PLC* has the steps and notes for developers (in English
and Thai).

Without a PLC, the tests contain a fake PLC OPC UA server:

```powershell
$env:PLC_UA_SERVE = "4840"; go test -run TestUAServe -timeout 0
```

### Editing the config from the dashboard

You can change network settings and tags while the monitor runs, with no
restart:

- **Network settings** (header button or File menu) opens in **its own
  window**, which you can keep open beside the monitor. In the desktop app it
  is a separate Windows window, like all the program's windows (no address
  line). In a browser it is a popup window; if popups are
  blocked, the dialog opens inside the page instead. The window shows the live
  link state and has **Apply** (stay open), **OK** (apply and close) and
  **Close**. Settings: protocol (ISO-TCP, Modbus TCP/IP, Modbus RTU over RS485 or
  through a gateway), the simulator switch, connection settings, poll interval
  and reconnect timing. Applying reconnects with the new settings. If the
  settings are changed elsewhere (e.g. File → Open), the window picks them up
  when it gets focus, unless you are in the middle of editing. It closes by
  itself when PLC Monitor exits.
- **+ Add tag**: a form whose areas, types and bit range follow the protocol.
  It shows the resulting address as you type (e.g. `DB5.DBD8`, or
  `HR10 (40011)` for Modbus).
- **Edit / Delete** on each table row (or double-click a row): change or remove
  a tag. Changes apply at the next poll and do not reconnect. Tags whose
  address and type are unchanged keep their current value, change count and
  chart history.

Every change is checked first. Bad durations, duplicate names, a missing DB
number, a bit out of range, a type the area can't hold, or an area from the
other protocol are rejected with a message in the form. The change is then
kept in the workspace file given with `-config`, so it survives a restart.
Notes on the workspace file:

- The file is rewritten by the program, so **comments are lost**. The first
  save keeps your original file as `config.yaml.bak`.
- `-simulate` and `-listen` are command-line overrides and are not written to
  the file.
- While the simulator is on, the connection settings are kept but not used.
  Switching the simulator on or off in the dialog *is* saved to the file.

Set `web.read_only: true` to turn editing and writing values off: the edit and Write buttons are hidden, the
File menu's editing items are greyed out (Exit still works), and the edit API
returns 403. Charts still work.

### API

| Method & path | Body | Purpose |
|---|---|---|
| `GET /api/state` | | link status, open files, unsaved flags and live tag values |
| `GET /api/config` | | current network settings and tag list |
| `GET /api/history/{name}?since=ms` | | chart samples newer than `since` (Unix ms) as `[[ms, value or null], …]` |
| `GET /api/trend/{name}?from=ms&to=ms&max=n` | | samples from the history files for a period, reduced to about 2n points |
| `GET /api/trend/{name}/csv?from=ms&to=ms` | | download a period as CSV, full resolution |
| `GET /api/trends/csv?tag=a&tag=b&from=ms&to=ms` | | several tags side by side (1–16), one column each |
| `GET /api/ui/layout` | | charts and tag windows, with a revision number |
| `PUT /api/ui/layout` | `{"chart_place":"main","charts":[{"id","title","tags":[…],"window","scale","range_ms"}],"tag_windows":[{"id","title","tags":[…]}]}` | save the layout (this computer only) |
| `POST /api/history/folder` | `{}` | open the history folder in Explorer (this computer only) |
| `GET /api/netifs` | | this PC's IPv4 networks |
| `POST /api/scan` | `{"subnet":"192.168.0.0/24","timeout_ms":400,"units":false}` | start a network scan (this computer only) |
| `POST /api/scan/bus` | `{"from":1,"to":247}` | scan slave IDs on the current Modbus connection |
| `GET /api/scan` | | scan progress and results |
| `GET /api/dcp/adapters` | | whether PROFINET DCP is available (Npcap), and the adapters |
| `POST /api/dcp/identify` | `{"adapter":"…","wait_ms":3000}` | find PROFINET devices (this computer only) |
| `POST /api/dcp/set` | `{"adapter","mac","set_ip","ip","mask","gateway","set_name","name","permanent"}` | set IP parameters and/or name of station (this computer only, not read-only) |
| `POST /api/dcp/flash` | `{"adapter","mac"}` | flash the device's LEDs |
| `PUT /api/dcp/settings` | `{"source":"agent"\|"local","agent_url":"…","agent_token":"…"}` | where DCP runs (this computer only; the token is never sent back) |
| `POST /api/dcp/npcap/check`, `POST /api/dcp/npcap/download` | `{}` | load Npcap again after installing it; open npcap.com |
| `POST /api/plcscan` | `{"from":1,"to":1000,"areas":true,"block_list":false}` | Scan PLC: find data blocks (and M/I/Q) on the current ISO-TCP connection |
| `GET /api/plcscan` | | its progress and results: `found` = `[{"area":"DB","db":1,"size":68}, …]` |
| `DELETE /api/plcscan` | | stop it |
| `GET /api/plcscan/read?area=DB&db=1&start=0&size=64` | | read bytes (up to 64 KB), returned as base64 in `data` |
| `GET /api/export/grpc.proto` | | the gRPC API as a .proto file |
| `GET /api/export/preview` | | Data view: per tag the MQTT topic and payload and the InfluxDB line sent for its current value (this computer only) |
| `GET /api/export/broker` | | Data view: the last message on every topic under `<prefix>/#`, from a watching client (stops 45 s after the last call; `DELETE` stops it) |
| `GET /api/export/database` | | Data view: the last value of every tag read back from InfluxDB, and the query used |
| `POST /api/opcua/browse` | `{"endpoint","security","username","password"}` | read the PLC's tag list over OPC UA (empty password: the saved one) |
| `GET /api/opcua/browse` | | its progress and the variables: `name`, `node`, `folder`, `data_type`, `type`, `comment`, `s7` (the absolute address, if known) |
| `DELETE /api/opcua/browse` | | stop it |
| `POST /api/opcua/read` | `{"nodes":["ns=3;s=…", …]}` | live values of up to 2000 nodes |
| `POST /api/opcua/tagfile` | `[tag, …]` | the tags as a tag file (YAML) |
| `GET /api/plcscan/symbols` | | imported variable names: files, blocks with their variables and offsets |
| `POST /api/plcscan/symbols` | `{"name":"Line.db","data":"<base64>"}` | import a DB source or tag table (replaces a file of the same name) |
| `POST /api/tags/source` | `{"name":"x.scl","data":"<base64>"}` or `{"path":"…"}` (this computer only) | the data blocks of a block source with size, DB number (known or by size from the last scan) and their variables as tags: `{"blocks":[…],"found":[…]}`; adds nothing by itself |
| `GET /api/tags/scl` | | the DB tags as SCL data blocks: `{"data":"<base64>","count":n,"skipped":[…]}` |
| `GET /api/tags/tia?format=xlsx` (or `xml`, `sdf`) | | the tag list as a PLC tag table: `{"data":"<base64>","count":n,"skipped":[{"name","reason"}]}` |
| `POST /api/tags/tia` | `{"name":"PLCTags.xml","data":"<base64>"}`, or `{"path":"D:/PLCTags.xml"}` (this computer only) | read a PLC tag table (.xlsx, .xml, .csv) as tags: `{"tags":[…],"skipped":n}`; adds nothing by itself |
| `PUT /api/plcscan/symbols/db` | `{"block":"Line_Data","db":10}` | give a data block its number (0 removes it) |
| `DELETE /api/plcscan/symbols?file=Line.db` | | remove one file's names, or all without `file` |
| `POST /api/save-file` | `{"name":"x.png","data":"<base64>"}` | desktop app: Save as dialog for an export (.png/.jpg/.pdf) |
| `DELETE /api/scan` | | stop scans |
| `PUT /api/network` | `{"protocol","simulate","address","rack","slot","connection_type","timeout","modbus":{…},"poll_interval","reconnect_delay","reconnect_max"}` | change network settings (durations as strings, e.g. `"1s"`) |
| `POST /api/connection` | `{"connect": false}` / `{"connect": true}` | Disconnect (no traffic until connected again) / Connect; `/api/state` shows `"paused"` |
| `POST /api/tags` | a tag, e.g. `{"name":"P1","area":"DB","db":5,"offset":8,"type":"REAL"}` | add a tag |
| `PUT /api/tags/{name}` | a tag | replace (and optionally rename) a tag |
| `PUT /api/tags` | `[tag, tag, …]` | replace the whole tag list in one step, in that order (paste, delete several, insert, undo); all or nothing |
| `DELETE /api/tags/{name}` | | remove a tag |
| `POST /api/file/{network\|tags}/{new\|open\|save\|saveas}` | `{"path": "…"}` for open and saveas | File menu (from this computer only) |
| `POST /api/os/network-connections` | `{}` | open the Windows Network Connections window (this computer only) |
| `GET /api/os/adapters` | – | this PC's network adapters: name, state, DHCP, IPv4 addresses (Windows, this computer only) |
| `POST /api/os/adapters` | `{"adapter":"Ethernet 2","action":"add","ip":"192.168.1.200","mask":"255.255.255.0"}` | `add` / `remove` an IPv4 address; `static` (`ip`, `mask`, optional `gateway`, `dns` [up to 2]) or `dhcp`; `enable` / `disable` an adapter. Windows asks for administrator permission (this computer only, not read-only) |
| `POST /api/os/ping` | `{"ips":["192.168.1.1"],"timeout_ms":1000}` | ping up to 64 addresses at once: `alive`, `ms`, `ttl`, `self` (this computer only) |
| `POST /api/config/folder` | `{}` | File → Folders → Configuration folder: open it in Explorer (this computer only) |
| `POST /api/exit` | `{}` | File → Exit: stop the program (from this computer only) |
| `POST /api/write/{name}` | `{"value": 12}` or `{"value": true}` | write once (this computer, or others with `allow_remote_write`) |
| `POST /api/gen/{name}` | `{"kind":"sine","min":0,"max":100,"period_ms":10000,"interval_ms":500}`; BOOL: `{"kind":"toggle","on_ms":1000,"off_ms":1000}` or `{"kind":"tick","width_ms":100,"period_ms":1000}` | start (or replace) a signal generator |
| `DELETE /api/gen/{name}`, `DELETE /api/gen` | | stop one generator, or all |
| `GET /api/serialports` | | COM ports Windows currently has |
| `GET /api/export` | | export settings and status (this computer only: contains passwords and tokens) |
| `PUT /api/export` | `{"csv":{…},"mqtt":{…},"influxdb":{…,"flush_interval":"5s"}}` | change export settings, applied at once (this computer only) |
| `GET /api/export/status` | | state of each exporter: ok / connecting / error, sent, pending, last error |
| `POST /api/export/test/{csv\|influxdb\|mqtt}` | the same body as `PUT /api/export` | Test connection; returns the steps, server version, databases/buckets and MQTT messages |
| `POST /api/export/create/influxdb` | the `influxdb` object | create the configured database (v1, v3) or bucket (v2) |
| `POST /api/export/csv/folder` | `{"path":"…"}` | open the CSV file's folder in Explorer |
| `GET /export`, `GET /help`, `GET /plcscan`, `GET /plctags` | | the Export settings, Help, Scan PLC and PLC tags windows |
| `GET /themes.js` | | the themes and theme picker, preceded by the saved theme |
| `PUT /api/ui/theme` | `{"theme":"nord"}` (`"system"` follows Windows) | save the theme in config.yaml (this computer only) |

The DCP agent (`plcmonitor -dcp-agent :8095`) serves the same `/api/dcp/adapters`,
`identify`, `set` and `flash` with the header `Authorization: Bearer <token>`.

Edit requests must send `Content-Type: application/json`, which stops other web
sites from making edits through your browser.

### Security

The dashboard has **no login**, and anyone who can reach it can change what is
monitored and where the program connects. Opening and saving files is limited
to the computer running PLC Monitor, but other edits are not. On a shared or
plant network, either bind it to `127.0.0.1:8080` or set
`web.read_only: true`.

## Tests

```bash
go test ./...
```

The tests cover decoding and encoding of every data type, absolute address
formatting, block grouping, deadbands and NaN handling, a full
connect → poll → link drop → reconnect cycle against the simulator, live tag
edits (add/retype/delete without reconnecting), the config edit API including
validation errors, saving and reloading the config file, and the InfluxDB line
protocol against a fake HTTP server.

They also cover:

- **Modbus TCP over a real socket:** the tool polls the Modbus library's own
  server on loopback for every area and data type, including register bits.
  An "illegal data address" reply marks only the affected tags bad and does
  not drop the connection.
- **Modbus details:** word order, address limits, and grouping into requests
  with and without max gap.
- **File menu:** New / Open / Save / Save as for both kinds, unsaved-change
  tracking, switching protocol with tags of the other protocol, and refusing
  a file of the wrong kind.
- **History and remote access:** the chart history API, and the File API
  refusing non-local callers.
- **OPC UA against a fake PLC OPC UA server:** browsing (names, areas, types,
  comments, structures and arrays), monitoring and writing UA tags, a missing
  node, and mapping names to absolute addresses.
- **Scan PLC and names:** finding and measuring data blocks on the simulator,
  DB source layout (structures, arrays, PLC data types, strings), tag tables
  (.xlsx, .xml as tag-table export and Openness XML export, .csv), importing a tag table as tags, and
  the names store.

## Not verified against real hardware

This was built and tested without a PLC, broker or database. These parts are
unverified:

- **Real ISO-TCP traffic.** The gos7 connection code (`plc.go`) has not talked
  to actual 300/400/1200/1500-series CPUs. The simulator replaces the network layer and
  feeds the same byte decoding, but it does not exercise ISO-on-TCP, PDU
  negotiation, TSAP/rack/slot handling or PLC error codes.
- **How a real link drop is detected.** With a real PLC, a pulled cable
  surfaces as a read timeout (`plc.timeout`), after which the tool reconnects.
  This was only tested with the simulator's forced drops.
- **Modbus against real devices.** Modbus TCP was tested only against the
  library's own server on loopback. The RS485 handling was tested over RTU over
  TCP against a fake bus with several slaves that speaks real RTU frames with
  CRC: per-slave IDs, a silent slave, and a completely silent line. **The serial
  COM port path (`rtu` mode) has not been run**, because this PC has no COM port
  or RS485 adapter. It uses the same RTU framing, but baud rate, parity, adapter
  drivers and line timing can only be checked on site. The same goes for
  device quirks (unit ID 0/255 on gateways, unusual word orders, slow slaves
  needing a longer timeout).
- **Writing to real devices.** Modbus writes (coil, register, multi-register
  REAL, register bit) were tested against the Modbus library's server on
  loopback. Writes through the poll loop and all generators were tested with
  the simulator. **ISO-TCP writes have never reached a real PLC.** That includes
  the single-bit write for BOOL and the byte writes for other types. 1200- and
  1500-series CPUs also need PUT/GET enabled and the DB not "optimized", the same
  as for reading, and a write-protected CPU will refuse. RS485 writes over a
  real line are also untested.
- **MQTT.** The exporter and the Test connection button were tested against a
  small MQTT 3.1.1 broker written for the tests. That covered login, a wrong
  password, QoS 1, retained values, `json` and `value` payloads, the last-will
  status, and the reason shown while it can't connect. **No real broker
  (Mosquitto, EMQX, HiveMQ) has been used**, and TLS (`ssl://`, `wss://`) and
  WebSocket have not been run at all.
- **InfluxDB.** For all three versions, these were checked against fake HTTP
  servers: the endpoint, query, login header, health check and line format.
  The Test connection steps were checked too: version detection, a rejected
  token, an unknown organization, a missing bucket/database, and Create.
  Nothing was sent to a real InfluxDB 1.x, 2.x or 3. The answers of the real
  servers (for example the v3 database list format) may differ from the fakes.
- **Topology scan on a real plant network.** The scan, Modbus gateway
  detection and the slave-ID scan were tested against Modbus servers on this
  PC. **The ISO-TCP identification (CPU type, order number, firmware, RUN/STOP) has
  never met a real PLC.** What a CPU reveals depends on model and protection
  level.
- **PROFINET DCP (find devices, set IP address / name, flash LED).** These
  were checked with unit tests against byte-exact frames:
  - Identify and Set requests, and answers with and without a VLAN tag;
  - refusals and their error codes;
  - the name and IP rules;
  - the API, including the permission checks.

  **It has never run on a real network**, because this PC has no Npcap and no
  PROFINET device. Untested: the Npcap binding (`dcp_windows.go`), how
  real devices answer, and whether a given CPU accepts a change. Try it first
  on a device that is not in production.
- **Linux agent.** The full path Windows dashboard → agent → device was tested
  with a simulated device, covering the token, wrong token, agent down and
  device refusals. The Linux builds compile and pass `go vet`. **They have
  never run on Linux** (this PC has no Linux or WSL), so these are untested:
  the raw socket (`dcp_linux.go`), `install-agent` / `uninstall-agent`, the
  systemd service and the automatic sudo restart. Run `install-agent` on a test
  machine first and check `systemctl status plcmonitor-dcp`.
- **Desktop app.** It was run on Windows 11 at 100% display scaling only. These
  were not tried: window sizing at 125–200% scaling, Windows 10, and a PC
  without the WebView2 Runtime (the message box path).

When you first connect to a real PLC, start with one or two tags whose values
you know (for example a marker word you can change from the PLC engineering software). Then check
that the dashboard shows the same values before you add the full list.
