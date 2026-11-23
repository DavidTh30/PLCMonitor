# Changelog

## 1.4.0

### New

- **Scan with "all" also searches with PROFINET DCP** on every adapter at
  once, in the same scan: PLCs and other PROFINET devices on each cable are
  found whatever their IP address (like a PROFINET service tool), each with
  whether this PC can reach it over IP – and if not, which address to add to
  which adapter, or *Configure…* to give the PLC an address in this PC's
  network. On Windows this needs Npcap (or the Linux agent): without it the
  tree says so with **Download Npcap** / **Check again**. When no Linux agent
  is set up and Npcap is there, DCP runs from this PC.
- **Network adapters window** (Windows; ⚙ menu, File → PLC, Topology): this
  PC's adapters with icons, state (connected / no link / disabled), type,
  MAC, speed, gateway, DNS and IPv4 addresses – inside PLC Monitor, without
  the Network connections window.
  - **Add address** / **×** remove a fixed address (Windows keeps the
    others: one cable, several networks), with **Check free** (ping first).
  - **IP settings:** *Automatic (DHCP)* or *Manual* – IP address, mask,
    gateway, DNS, like Windows' IPv4 properties.
  - **Enable / Disable** an adapter.
  - Helpers: **Reach a PLC** (type the PLC's address: is this PC in its
    network? a free address next to it, the wired adapter, one button to add
    it, then a ping), **Ping** (lists, ranges, repeat every second),
    **Subnet calculator**, **Saved settings** (named settings per machine or
    customer, applied with one click), **Copy** (all adapters as text).
  - Windows asks for administrator permission (UAC) for each change; PLC
    Monitor itself keeps running without it. Every change is logged.
- **One click to reach a PLC in another network:** where the topology says
  *Not reachable* (the PLC in Network settings, a PROFINET device) there is
  now a button **Add 192.168.1.200 to Ethernet 2** with the suggested
  address. When it isn't clear which adapter is on the PLC's cable, the
  Network adapters window opens on *Reach a PLC* for that device.

### Changed

- **Topology, "all":** also looks at the PLC set in Network settings, first
  in the tree (*192.168.1.1 – the PLC in Network settings*), even when it is
  outside this PC's networks. When this PC has no address in that network
  the branch says *Not reachable*, which address to add (Windows keeps the
  one it has: IPv4 → Advanced → Add) and that PROFINET devices finds PLCs on
  the cable whatever their address.
- An OPC UA server that closes the connection to a guest scan ("EOF") is
  explained: it probably accepts only known clients (a trusted certificate
  or a user name).

## 1.3.3

Topology finds everything in one scan – all of this PC's networks and
programs on this PC itself – shows each PLC's version and clock, and says
why a network can't be reached.

### Downloads

| File | For |
|---|---|
| `PLCMonitorSetup-1.3.3-x64.exe` | Windows installer, 64-bit (most PCs) |
| `PLCMonitorSetup-1.3.3-x86.exe` | Windows installer, 32-bit Windows |
| `PLCMonitorApp-x64.exe` / `PLCMonitorApp-x86.exe` | Windows desktop app, a single file (64 / 32-bit) |
| `plcmonitor-x64.exe` / `plcmonitor-x86.exe` | Windows console program with web dashboard (64 / 32-bit) |
| `plcmonitor-linux-amd64` / `-386` | Linux PC, 64 / 32-bit |
| `plcmonitor-linux-arm64` / `-armv7` | Raspberry Pi and other ARM boards, 64 / 32-bit OS |

### Not yet verified

- Reading the clock of a real CPU over ISO-TCP: tested with a recorded
  answer and a virtual PLC (which does not give its clock); the real CPU was
  not connected. Over OPC UA the clock of a real 1217C was read.

### New

- **Topology finds everything in one scan:** *Network to scan* starts as
  **all** – every network of this PC with a link – and also takes several
  networks separated by commas. **This PC (127.0.0.1)**, ticked by default,
  finds programs on this PC itself: virtual PLCs, simulators, gateways. The
  tree has a branch per network, a *This PC* branch, and a branch for an
  adapter without a link saying so. Devices at an address of this PC are
  marked *on this PC*. API: `POST /api/scan` takes `"subnet":"all"` and
  `"local":true`; devices carry `network` and `self`.
- **Version and PLC clock** on every PLC card. The clock is read over
  ISO-TCP ("read clock", reading only) or from the OPC UA server, and shown
  with how far it is from this PC's clock – in red with *set the time* when
  it is a minute or more off (e.g. a CPU still at 2012). A PLC that does not
  give them (e.g. a virtual PLC that answers "function not supported") shows
  "not reported by this PLC".

### Changed

- **Topology** shows network adapters that have an address but **no link**
  (cable unplugged, switch or PLC off), e.g. `192.168.1.200 (Ethernet 2) –
  no link: cable?`, and reads this PC's networks again every 5 s instead of
  only when the window opens (a PC that just woke up showed "no network
  found" until the window was reopened).
- A scan that finds nothing says why when it can tell: the adapter for that
  network has no link, or this PC has no address in it (with a Network
  connections… button).
- Network settings' *This PC* line marks adapters without a link too.
  `GET /api/netifs` gives them with `"down": true`.

## 1.3.2

Clean-up; no new features. The program works as in 1.3.1.

### Downloads

| File | For |
|---|---|
| `PLCMonitorSetup-1.3.2-x64.exe` | Windows installer, 64-bit (most PCs) |
| `PLCMonitorSetup-1.3.2-x86.exe` | Windows installer, 32-bit Windows |
| `PLCMonitorApp-x64.exe` / `PLCMonitorApp-x86.exe` | Windows desktop app, a single file (64 / 32-bit) |
| `plcmonitor-x64.exe` / `plcmonitor-x86.exe` | Windows console program with web dashboard (64 / 32-bit) |
| `plcmonitor-linux-amd64` / `-386` | Linux PC, 64 / 32-bit |
| `plcmonitor-linux-arm64` / `-armv7` | Raspberry Pi and other ARM boards, 64 / 32-bit OS |

### Changed

- The pages' source no longer names a PLC maker or its software in comments
  either (the screens already did not since 1.3.0).
- Source tidied: the tag table export file has a neutral name, a helper a
  clearer one; `*.exe~` (a copy Windows leaves when a running program is
  replaced) is ignored by git.

## 1.3.1

Data blocks from SCL sources – also instance DBs of function blocks – as
tags and back, .sdf tag tables, the OPC UA server of a PLC in Topology, and a
shortcut to Windows' Network Connections. Checked against a 1200-series CPU
(CPU 1217C, firmware V4.7): a 486-byte instance DB with five FB
multi-instances, timers and edge detectors matched to the byte, and 459 tags
from an SCL source read good values.

### Downloads

| File | For |
|---|---|
| `PLCMonitorSetup-1.3.1-x64.exe` | Windows installer, 64-bit (most PCs) |
| `PLCMonitorSetup-1.3.1-x86.exe` | Windows installer, 32-bit Windows |
| `PLCMonitorApp-x64.exe` / `PLCMonitorApp-x86.exe` | Windows desktop app, a single file (64 / 32-bit) |
| `plcmonitor-x64.exe` / `plcmonitor-x86.exe` | Windows console program with web dashboard (64 / 32-bit) |
| `plcmonitor-linux-amd64` / `-386` | Linux PC, 64 / 32-bit |
| `plcmonitor-linux-arm64` / `-armv7` | Raspberry Pi and other ARM boards, 64 / 32-bit OS |

### Not yet verified

- Generating blocks from the exported SCL source, and importing the exported
  .xlsx / .xml / .sdf tag tables, in the PLC engineering software (none on
  the test PC); the files read back identically in PLC Monitor, and the .sdf
  export equals the engineering software's own file byte for byte.
- Counters (CTU, CTD, CTUD) in instance DBs: laid out from the documented
  structure, not checked on a PLC.
- Reading tags over OPC UA from a PLC that publishes them: the test PLC's
  standard server interface was off; Topology reports that correctly.
- The installer's move of an S7 Monitor installation (from 1.3.0) is still
  not run on a PC with one installed.

### New

- **Topology shows OPC UA servers on PLCs:** every device with port 4840 is
  asked (read only) for its endpoint, security modes and guest access, and –
  with guest access – for its identity (model, order number, firmware,
  serial) and whether it publishes the PLC's tags. When it publishes none,
  the card says what to tick (*Enable standard server interface*). Buttons
  **PLC tags (OPC UA)…** (opens the window on that endpoint) and **Monitor
  via OPC UA**. Before, a PLC with port 102 was only treated as an ISO-TCP
  PLC and its OPC UA server was just a port number.
- A CPU that does not give its identity over ISO-TCP gets it from its OPC UA
  server (checked on a 1217C: model, order number, firmware, serial).

- **SCL sources, both ways:**
  - **File → Import → Data blocks from SCL source (.scl / .db / .udt)…**
    (also File → Tag configuration → Open…): a dialog lists the data blocks
    of the source – also instance DBs of FBs – with size and DB number. The
    number comes from Scan PLC's names or, by size, from the PLC (**Find DB
    numbers on the PLC** scans it, read only), and the size is checked
    against the PLC. The ticked blocks' variables become tags
    (`Block.Variable`) in one undo step. API: `POST /api/tags/source`.
  - **File → Export → Data blocks as SCL source (.scl)…**: the tags in data
    blocks as SCL data blocks with standard access, padded with `Spare_…`
    members so every tag keeps its address – for *Generate blocks from
    source*. Each block is read back before it is written. Tags that can't
    fit (outside DBs, overlapping, a 16-bit value at an odd byte) are left
    out and counted. API: `GET /api/tags/scl`.
  - Checked on a 1200-series CPU: FB_FluffSilo_DB.scl → DB10 and DB13
    found by size, 459 tags all reading good values; written back as SCL and
    read again, all 459 addresses equal.
- **Instance DBs of function blocks** (Scan PLC → Variable names): an
  instance DB is laid out from its FB's source – inputs, outputs, in/outs and
  statics, multi-instances of other FBs, and the built-in timers (TON, TOF,
  TP, TONR and the _TIME forms; PT and ET readable), counters (CTU, CTD,
  CTUD) and edge detectors (R_TRIG, F_TRIG). Checked against a 1200-series
  CPU: a 486-byte instance DB with five FB multi-instances, eleven timers and
  edge detectors matched to the byte, with sensible live values. A DB whose
  FB source is missing says which FB to import.
- **PLC tag tables as .sdf** (one quoted line per tag): File → Import → PLC
  tag table (.xlsx / .xml / .sdf), File → Tag configuration → Open…, Scan
  PLC → Variable names, and File → Export → **PLC tag table (.sdf)**; the
  export matches the engineering software's own file byte for byte.
- Topology no longer says "enable PUT/GET" for a CPU that only withholds
  its identity: it reads one memory byte to tell, and says "reading works
  (PUT/GET is on)" when it does.
- **Windows network connections…** (File → PLC, the ⚙ menu, Network
  settings, Topology): opens Windows' Network Connections window to set this
  PC's IP address. Network settings also shows this PC's addresses (updated
  every 5 s, e.g. after plugging in a cable). API: `POST
  /api/os/network-connections`.

## 1.3.0

**S7 Monitor is now PLC Monitor.** The program no longer shows the names of
one PLC maker or its software: the connection type is called **ISO-TCP**
(port 102), CPU families are named by series (1200/1500-series, 300/400-series)
and the vendor's engineering tool is "the PLC engineering software". Also new:
PLC tag tables (.xlsx / .xml) in and out, a File menu in submenus, CSV import
and export of the tag list, and a tag table that scrolls on its own.

### Downloads

| File | For |
|---|---|
| `PLCMonitorSetup-1.3.0-x64.exe` | Windows installer, 64-bit (most PCs); moves an S7 Monitor installation |
| `PLCMonitorSetup-1.3.0-x86.exe` | Windows installer, 32-bit Windows |
| `PLCMonitorApp-x64.exe` / `PLCMonitorApp-x86.exe` | Windows desktop app, a single file (64 / 32-bit) |
| `plcmonitor-x64.exe` / `plcmonitor-x86.exe` | Windows console program with web dashboard (64 / 32-bit) |
| `plcmonitor-linux-amd64` / `-386` | Linux PC, 64 / 32-bit |
| `plcmonitor-linux-arm64` / `-armv7` | Raspberry Pi and other ARM boards, 64 / 32-bit OS |

### Not yet verified

- The installer's move of an existing S7 Monitor installation (settings, tag
  files, history, shortcuts, Apps entry) was built but not run on a PC with
  S7 Monitor installed. The move of the window data folder was tested.
- The Linux agent's takeover of an `s7monitor-dcp` service was not run.
- Importing the exported .xlsx / .xml into the PLC engineering software was
  not tried (no engineering software on the test PC); the files read back
  identically in PLC Monitor, and the .xlsx is a valid Office file.

### Renamed

- Program files: `PLCMonitorApp.exe` / `PLCMonitorApp-x86.exe`,
  `plcmonitor.exe` / `plcmonitor-x86.exe`,
  `PLCMonitorSetup-<version>-x64.exe` / `-x86.exe`, `plcmonitor-linux-amd64`
  / `-386` / `-arm64` / `-armv7`; the log is `plcmonitor.log`.
- **Installer:** installs to `%LOCALAPPDATA%\Programs\PLC Monitor` with the
  Start menu entry and Apps entry *PLC Monitor*. An installation of
  S7 Monitor is moved: its settings, tag files, trend history and imported
  names go to the new folder, and its program files, shortcuts and Apps entry
  are removed. The window's data (theme, layout, recent files) moves from
  `%LOCALAPPDATA%\S7Monitor` to `%LOCALAPPDATA%\PLCMonitor`.
- **Linux agent:** `plcmonitor install-agent` installs the service
  `plcmonitor-dcp` (token in `/etc/plcmonitor/agent.token`). It takes over the
  token of an `s7monitor-dcp` service and removes that service.
- **Outputs:** the defaults for new exporters are `plcmonitor` (MQTT client ID
  and topic prefix, AMQP routing prefix), measurement `plc` (InfluxDB,
  VictoriaMetrics) and table `plc_values` (QuestDB, TimescaleDB, ClickHouse).
  Settings already saved keep their names. The servers now publish
  `plcmonitor_value` / `plcmonitor_good` / `plcmonitor_last_change_seconds`
  (Prometheus), the gRPC service `plcmonitor.v1.Monitor`, and the OPC UA
  folder `Objects/PLCMonitor`; clients and dashboards using the old names
  need updating.
- **OPC UA client:** connects as *PLC Monitor*. A client certificate made by
  S7 Monitor (which the PLC may already trust) keeps working.
- Topology shows PROFINET vendor ID 0x002A as "Vendor 002A".
- Unchanged for compatibility: in config files `protocol: s7` still selects
  ISO-TCP, and the API paths and JSON field names stay the same.

### New

- **PLC tag table (.xlsx / .xml)…** (File → Import): adds the I, Q and M
  tags of a PLC tag table exported from the PLC engineering software to the
  tag list, with names, types and comments. Tags already in the list (same
  name) are updated instead of copied; one undo step. API: `POST
  /api/tags/tia`.
- **File → Tag configuration → Open…** also lists PLC tag tables
  (.xlsx, .xml, .csv); picking one imports it the same way instead of
  replacing the tag configuration.
- **PLC tag table (.xlsx) / (.xml)…** (File → Export): the
  tag list as a PLC tag table. The .xlsx has the sheet *PLC Tags* with the
  columns of the engineering software's tag table Export (for PLC tags →
  Import there); the .xml is in the `<Tagtable>` form. Only I, Q and M tags fit in
  a tag table; DB, Modbus, OPC UA and LReal tags are left out and counted.
  API: `GET /api/tags/tia?format=xlsx|xml`.

- **File menu** items added: **Save all** (Ctrl+S), **Open…** for tags with
  Ctrl+O, **recent files** for both kinds, **Tag list as CSV (Excel)** and
  **Tag list from CSV** (every field; tags with the same name are replaced,
  one undo step), **Variable names for Scan PLC**, **Topology**, and
  **Configuration folder** (API: `POST /api/config/folder`).

### Changed

- **File menu** grouped in submenus: Tag configuration, Network
  configuration, Import, Export, PLC and Folders, with Windows icons and
  shortcuts.
- **Main window and tag windows:** only the tag table scrolls. The title
  bar, menu, header and toolbar stay in place, and the column headings stay
  at the top of the table. Charts take up to half the window and scroll
  among themselves. A very low window (under 420 px) scrolls as a whole, as
  before.
- PLC tag tables exported as **XML** are read: the tag table's own Export
  (`<Tagtable>`, e.g. from engineering software V17) and the Openness XML
  export (`SW.Tags.PlcTag`). This
  also works in Scan PLC → Variable names.

## 1.2.1

Tested against the OPC UA server of a real S7-1200 (CPU 1217C, firmware
V4.7, TIA V17): connecting without security and as guest works.

### Downloads

| File | For |
|---|---|
| `S7MonitorSetup-1.2.1-x64.exe` | Windows installer, 64-bit (most PCs) |
| `S7MonitorSetup-1.2.1-x86.exe` | Windows installer, 32-bit Windows |
| `S7MonitorApp-x64.exe` / `S7MonitorApp-x86.exe` | Windows desktop app, a single file (64 / 32-bit) |
| `s7monitor-x64.exe` / `s7monitor-x86.exe` | Windows console program with web dashboard (64 / 32-bit) |
| `s7monitor-linux-amd64` / `-386` | Linux PC, 64 / 32-bit |
| `s7monitor-linux-arm64` / `-armv7` | Raspberry Pi and other ARM boards, 64 / 32-bit OS |

### Changed

- **PLC tags (OPC UA)** window: when the PLC answers but publishes no tags,
  it says why instead of showing an empty list. On a SIMATIC PLC this means
  *Enable standard SIMATIC server interface* is off in TIA Portal (CPU
  properties → OPC UA → Server; S7-1200 firmware 4.5+, S7-1500 2.9+); the
  message says what to tick and to download Hardware and software. The API
  (`/api/opcua/browse`) reports it as `hint`.
- Help, *Switch on OPC UA in TIA Portal*: the step for the standard SIMATIC
  server interface is added (English and Thai).

## 1.2.0

Tag names from the PLC (OPC UA, or files from TIA Portal), a scan of the
PLC's data blocks, nine more export targets, a Data view of what is sent,
and a desktop app with its own title bars. Now also built for 32-bit
Windows and 32-bit Linux.

### Downloads

| File | For |
|---|---|
| `S7MonitorSetup-1.2.0-x64.exe` | Windows installer, 64-bit (most PCs) |
| `S7MonitorSetup-1.2.0-x86.exe` | Windows installer, 32-bit Windows |
| `S7MonitorApp-x64.exe` / `S7MonitorApp-x86.exe` | Windows desktop app, a single file (64 / 32-bit) |
| `s7monitor-x64.exe` / `s7monitor-x86.exe` | Windows console program with web dashboard (64 / 32-bit) |
| `s7monitor-linux-amd64` / `-386` | Linux PC, 64 / 32-bit |
| `s7monitor-linux-arm64` / `-armv7` | Raspberry Pi and other ARM boards, 64 / 32-bit OS |

### Not yet verified against real systems

Developed and tested with simulators, test servers and unit tests:
reading tag names over OPC UA from a real S7-1200/1500 (its OPC UA server
was off), TimescaleDB, AMQP (no PostgreSQL or RabbitMQ was available), and
the other export targets against real servers. Scan PLC was tested against
the real S7-1200/1500. The 32-bit builds were built, not run.

### Changed

- **Desktop app:** the other windows (Network settings, Topology, Export,
  Help, Scan PLC, PLC tags, charts, tag windows, Status) are plain windows of
  the program, without the read-only address line that WebView2 popups
  showed.
- **Desktop app title bar:** the main window draws its own title bar with
  the app name, the link state (green when connected, red when not, grey
  when disconnected by you), a ⚙ menu (Network, Export, PLC tags, Theme,
  Always on top) and minimise / maximise / close. Dragging, double-click to
  maximise, Snap and resizing work as before. The other windows get the
  same style of title bar (icon, window name, minimise / maximise / close).
- **Network settings** window: as tall as its form (no empty space), and a
  *Send changes to* line showing every export target, on or off; a click
  opens its settings.

### New

- **Scan PLC** window (File → Scan PLC for data blocks / tags…):
  - finds the readable data blocks of an S7 PLC and their sizes, plus the M,
    I and Q areas;
  - shows the bytes as BOOL, BYTE, INT, WORD, DINT, REAL, LREAL, … live;
  - adds the rows or bits you pick as tags, named after their address.
  - **Variable names** from TIA Portal exports: DB sources (.db/.udt/.scl/
    .awl, offsets worked out for non-optimized blocks) and PLC tag tables
    (.xlsx/.csv). A Names view shows name, type, live value and comment, and
    the tags get those names and comments.
  - API: `/api/plcscan`, `/api/plcscan/read`, `/api/plcscan/symbols`.
- **Nine more export targets**, each with its own tab, Test connection and
  status in Export settings:
  - databases: **VictoriaMetrics** and **QuestDB** (line protocol),
    **TimescaleDB** (PostgreSQL, COPY, optional table and hypertable),
    **ClickHouse** (HTTP, JSONEachRow, optional MergeTree table); batched,
    kept and sent again when the server is down;
  - messaging: **AMQP** (RabbitMQ, routing key `<prefix>.<tag>`) and
    **CoAP** (POST/PUT over UDP, confirmable or not);
  - servers that S7 Monitor runs itself: **Prometheus** `/metrics`,
    **gRPC** (`s7monitor.v1.Monitor`: List, Subscribe; with reflection) and
    an **OPC UA server** (`Objects/S7Monitor/<tag>`, read-only).
- **Data view** window (File menu, ⚙ menu, or *Data view…* in Export
  settings): how the tags look on the other side.
  - **MQTT:** for every tag the topic and payload sent, next to what the
    broker holds now (a separate client subscribes to `<prefix>/#`),
    marked ✔ when the values agree; other topics such as `status` too.
  - **InfluxDB:** for every tag the line written (line protocol), next to
    the last point read back from the database, and the query used
    (InfluxQL, Flux or SQL) to copy into Grafana.
  - API: `/api/export/preview`, `/api/export/broker`, `/api/export/database`
    (this computer only).
- **OPC UA** (S7-1500, S7-1200 firmware 4.4+):
  - a third connection type next to S7 and Modbus: tags are read and written
    by node ID, so names come from the PLC and optimized DBs work;
  - **PLC tags (OPC UA)** window: read the tag list from the PLC with names,
    types, comments and live values; add as OPC UA or S7 tags; **Save as tag
    file**, **Export CSV**, **Compare / update** the tag list against the PLC;
  - security None / Sign / Sign & encrypt, anonymous or user name; a client
    certificate is created next to the config.
  - Help: *Tag names from the PLC*, with notes for developers (English and
    Thai).
  - API: `/api/opcua/browse`, `/api/opcua/read`, `/api/opcua/tagfile`.

## 1.1.0

The tag table works like a TIA Portal watch table, and the main window is
quieter. Tested against a real S7-1200/1500 (192.168.1.1, rack 0 slot 1):
reading, Modify writes, and Connect/Disconnect.

### New

- **Connect / Disconnect button** in the top bar.
  - Disconnect closes the link at once and stops all traffic (no reads,
    reconnect attempts or writes) and shows a grey OFFLINE label.
  - Connect reconnects immediately.
- **Modify column** (like *Modify value* in a watch table): prepare a value
  per tag.
  - **Enter** or **▶** writes one value.
  - **⚡ Modify selected** (Ctrl+Enter) and **⚡ Modify all** write several,
    with one confirmation listing them all on a real device.
  - Each box turns green when written, red with the reason when refused.
- **Tag table editing:**
  - Selection with click, **Ctrl+click** and **Shift+click** (also Ctrl+A,
    arrow keys).
  - **Copy / Cut / Paste**, also to and from **Excel** (tab-separated, any
    column order).
  - **Delete**, **Insert** (after the selection), **Add**, and **Undo /
    Redo** (100 steps).
  - A right-click menu with all of these.
- **Address helper** in the tag dialog:
  - Type `DB1.DBD4`, `MW10`, `I0.1`, `%Q2.0`, `E0.1`, `40001`, `HR3.4`,
    `U2:40001`, … and the fields are filled in.
  - **Next free** gives the next unused address.
  - Overlaps with existing tags are pointed out.
  - The name follows the address until you type your own.
  - Insert/Add suggest the next address and name (`M0.2` → `M0.3`).
- **Column chooser:** click a column heading or **⚙ Columns** to show or
  hide Address, Type, Value, Modify, Description and the buttons. The choice
  is saved.
- **Status window** (Status → Open status window): pick from 16 items in the
  **Status** menu, such as read time with min/avg/max and a trend line, polls
  per second, tags good/bad, reconnects, exporters and more. The choice is
  saved.

### Changed

- **The table:**
  - no colour flash on value changes;
  - the *Changes* and *Last change* columns are removed;
  - the row **Write** button is gone. Write in the Modify column; signal
    generators are under right-click → *Write… / signal generator*.
- The poll statistics line left the main header; it is in the Status window
  now.
- Deleting tags no longer asks for confirmation, because Ctrl+Z restores
  them.
- API additions:
  - `PUT /api/tags` replaces the whole list in one step;
  - `POST /api/connection` connects or disconnects;
  - `/api/state` reports `paused`, `read_ms` and `polls`.

## 1.0.0

First release of S7 Monitor: a monitoring tool for Siemens S7 PLCs and Modbus
devices, as a Windows desktop app, a console program with a web dashboard,
and Linux builds.

### Downloads

| File | For |
|---|---|
| `S7MonitorSetup-1.0.0.exe` | Windows installer (per user, no admin rights). Contains everything below. |
| `S7MonitorApp.exe` | Windows desktop app, a single file to run from any writable folder |
| `s7monitor.exe` | Windows console program with web dashboard |
| `s7monitor-linux-amd64` / `-arm64` | Linux: dashboard, or the PROFINET DCP agent (`install-agent`) |

### Features

- **Connections:**
  - Siemens S7-300/400/1200/1500 over ISO-on-TCP, with rack/slot.
  - Modbus TCP, Modbus RTU over RS485 on a COM port, and RTU over TCP through
    gateways, with several slave IDs on one line.
  - A built-in simulator, which is off by default.
- **Tags:**
  - DB, I, Q and M areas; HR, IR, COIL and DI for Modbus.
  - Types BOOL through LREAL, with deadbands and slave IDs.
  - Tags can be edited live while monitoring.
  - Network and tag configurations are saved as separate files (File menu).
- **Live table:**
  - Manual writes, plus signal generators: Boolean toggle and pulse;
    sine, square, sawtooth and random for numbers.
  - Extra **tag windows**, each with its own set of tags.
- **Charts:**
  - Up to 16 tags per chart, on a shared scale or one scale per tag.
  - BOOL tags get on/off lanes.
  - Drag a tag row onto a chart to add it, or drag one chart onto another to
    combine them.
  - A chart shows in the main window or in its own window.
  - Trend history files (CSV per tag per day, 30 days) with ranges up to 30
    days, browsing back in time, and CSV export.
- **Export:**
  - Every change goes to CSV, InfluxDB 1.x/2.x/3 and MQTT.
  - The Export settings window has quick setups, Test connection with hints,
    and creates the InfluxDB database or bucket when it is missing.
- **Topology:**
  - Network scan for S7 (CPU type, order number, RUN/STOP), Modbus TCP and
    gateways (slave IDs), EtherNet/IP, OPC UA and web.
  - Modbus slave-ID scan on the current connection.
  - Export of the view as PNG, JPG or PDF.
  - **PROFINET devices** panel (DCP): finds devices, including ones without
    an IP address, sets their IP address and name of station, and flashes
    their LEDs.
  - This works through a **Linux agent** (no driver on the Windows PC; set up
    with `install-agent`, runs with `CAP_NET_RAW` only, protected by a token),
    or through Npcap on the Windows PC itself.
- **Desktop app:**
  - 25 colour themes.
  - Always on top (📌, Ctrl+T), which also keeps its other windows on top.
  - Help window in English and Thai, with illustrated guides for Modbus,
    InfluxDB and MQTT.
- **Windows installer:**
  - Per-user install, with Start menu and desktop shortcuts and an entry in
    Installed apps.
  - Updates keep your data; uninstall keeps settings unless you tick the box.
  - Silent mode with `/S`, `/D=` and `/purge`.

### Not yet verified against real hardware

These were developed and tested with simulators, fake servers and unit tests:

- real S7 PLC traffic;
- real Modbus RTU on a COM port;
- real InfluxDB and MQTT brokers;
- PROFINET DCP on a real network, both through Npcap and through the Linux
  agent (the Linux builds have not been run on Linux yet).

See "Not verified against real hardware" in the README.

The executables are **not code-signed**, so Windows SmartScreen may warn on
first start ("More info" → "Run anyway").
