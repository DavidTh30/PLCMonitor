# Changelog

## 1.7.0

### New

- **IO test in the Topology window** (PROFINET device → details → **IO
  test…**): connect as the device's IO controller without typing commands.
  Inputs show as lamps per bit; outputs are switches per bit that work after
  **Arm outputs…** (with a warning). State, frame counters and timing, and
  the messages (alarms, module differences) are shown live. ✕ or Esc stops
  the test and releases the device. If the window stops answering, the
  outputs are disarmed after 3 s and the device is released after 15 s.
  Modules (default: the p-net sample, DI 8 / DO 8), cycle time and ports are
  remembered per device. Without Npcap the dialog offers *Download Npcap* /
  *Check again*.
- **Allow in firewall…** in the IO test: when Windows Firewall has no rule
  for UDP 34964 (where the device answers), one click adds it – Windows asks
  for administrator permission.

## 1.6.0

### New

- **Topology upgrade**:
  - **Click a device** for a details panel: IP with live ping (and a TTL
    guess of the system), MAC address and maker, DNS name, whether this PC
    can reach it, the neighbour-table state, open ports and what they are,
    the PLC / Modbus / OPC UA identity, its PROFINET (DCP) data and what its
    cable neighbour announces (LLDP) – everything known about it in one
    place, with its actions (Monitor, PLC tags, Flash LED, Set IP / name,
    open its web page, copy the details).
  - **Other devices**: this PC's neighbour table (ARP), read through
    Windows (GetIpNetTable2) or Linux – PCs, phones, routers, ESP32 boards
    and everything else without an industrial port, per adapter, with the
    maker from the MAC (or “private / random MAC”), the DNS name and a ping
    to tell devices that are there from old entries. Also in the PNG / PDF.
  - **PROFINET like PRONETA**: besides Find (DCP Identify, also without an
    IP address), Set IP / name of station and Flash LED: **Cable neighbour
    (LLDP)** – listens on an adapter for what the device or switch at the
    other end of the cable announces (name of station, port, management
    IP, link speed, PROFINET chassis MAC), also during “Scan all”; and
    **Reset to factory settings** (DCP ResetToFactory: IP and name, or all
    data; older devices get Factory reset) in the details panel. Through
    the Linux agent too (agents need updating for LLDP and reset).
  - **PROFINET IO test – first step** (console, not yet in the Topology
    window): `plcmonitor pnio-test -ip <device>` connects to a PROFINET
    device as IO controller (RT_CLASS_1, 32 ms by default), shows its inputs
    and forces its outputs after `arm`. No GSDML yet: the module layout
    defaults to the p-net sample (DI 8 / DO 8) and can be given with
    `-slots`. Needs Npcap (Windows) or a raw socket (Linux), and UDP port
    34964 open in the firewall. Not yet tried on a real device; design and
    plan in docs/profinet-io-test.md.

- **Alarm messages** (Tools → HMI → Alarm messages): a window to write and
  try HMI alarm texts from tags.
  - Each alarm: number, class (*fault*, *alarm*, *warning*, *info*), group,
    tag, condition – *TRUE* / *FALSE* (Bool), *bit n* (alarm words), *> >= <
    <= = <>* a limit, *outside* / *inside* a range – delay, acknowledge, and
    a text with `{value}` `{tag}` `{limit}` `{min}` `{max}`. Drag tags from
    the tag list to make alarms by type; **Samples** shows each kind.
  - **Signals**: every tag of the alarms simulated (TRUE/FALSE button, bit
    buttons, number / slider, toggle, ramp, sine, random) or read **live**.
  - Running alarms: a banner with the most important waiting alarm, the
    **Active** list (came / went / not acknowledged, **Ack** / **Ack all**)
    and the **History** of came, went and ack.
  - **Open / Save** (.plcalarm), **Import** and **Export** an alarm list as
    CSV or Excel (ID, Class, Group, Tag, Condition, Delay ms, Ack, Text);
    lists from other tools with *Bit*, *Limit*, *Message* columns are read too.
- **State graphics library**: 📚 *Library…* with 123 ready graphics in 20
  groups – LEDs, push buttons, relays, contactors, magnetic starters, motors,
  pumps, blowers, levels, alarm messages, valves (also the flat bow-tie
  valve), disconnect switches, circuit breakers, switches, selector
  switches, DIP switches, timers (TON / TOF / TP / TONR, progress ring,
  clock, countdown, hourglass …), flap valves (flap, damper, check flap,
  slide gate …), diverter valves (Y, with position switches, plug, rotary)
  and symbols (heater, heat exchanger, filter, cyclone, mixer, conveyors,
  airlock, compressor, hopper, scale, photo eye, flow line, instruments) –
  each with its states; *Use* it for the selected graphic or add it as a new
  one.
  - **With modes**: every graphic as Off (0) + Manual 1, Auto 2, Disable 3,
    Enable 4, Alarm 5, Warning 6, Fault 7, Interlock 8, Lock 9 – in the
    mode's colour with a letter badge; the mode colours can be changed (kept
    in the .plcstate file). **+ Modes** does the same for any graphic.
  - **🎨 Colours…** (for the graphic, or 🎨 on one state row): for all
    states or only one, each colour of the SVG image(s) can be kept, get a
    new colour, no colour, a pattern on it, colour + pattern or a coloured
    pattern (diagonal, cross-hatch, lines, grid, dots, checker, zigzag), and
    its own transparency; for the whole image line thickness, pattern size
    and line, brightness, a glow in any colour and opacity – with a before /
    after preview. Ctrl+Z undoes it.
- **State graphics** (Tools → HMI → State graphics): a window to make and
  try the images (PNG / SVG) that show a tag's state before they go into the
  HMI – a lamp off / on / fault, a motor, a valve, a tank level.
  - **Graphics** bound to tags: drag tags from the tag list onto it (a Bool
    gets Off / On, a number level ranges) or pick the tag; name and size.
  - **States**, checked from the top: *= value*, *from … to*, *bit n set*,
    *anything else*; an image each (click, or drop PNG / SVG files – several
    at once make a state each, `motor_2.png` → value 2), **Blink** for alarms.
    **Samples**: lamp, motor, valve, tank level, status bits as SVG.
  - **Try it**: the live tag value (PLC, simulator, or a signal generator on
    the tag), or a **simulated signal** – a value or slider, a button per
    state, step through the states, toggle 0/1, ramp, sine, random – at
    1× / 2× / 4×, and every graphic at once under *All graphics*.
  - **New / Open… / Save / Save as…** (.plcstate, Ctrl+O / Ctrl+S), the work
    kept between starts; **Import** images or graphics from another set /
    ZIP (a folder of images makes a graphic); **Export** a ZIP for the HMI
    (every image as a file + states.csv), the set, or the shown image as it
    is or as PNG.

- **UDT tool** (File → PLC → UDT tool, or right-click tags → *Send to UDT
  tool*): a small window to build a PLC data type (UDT) from the tags in use.
  - **Drag tags** from the tag list onto it (several selected at once): each
    becomes a member – name without the part they share (`Motor.Run` → `Run`),
    data type, comment, and the address it came from.
  - **Structures**: drag a member (⠿) **onto a Struct** to put it inside,
    between rows to place it there (out of a group too); **→** puts it into
    the Struct above, **←** takes it out; a Struct moves with its members.
    Tags dropped on a Struct go inside it.
  - **Edit**: name (dots make structures), data type (Bool, Int, Real,
    `String[20]`, `Array[0..7] of Bool`, Struct …), comment; move rows by
    dragging or ↑ ↓, insert, remove, + Struct, **Make arrays**
    (`Alarms[0]` … `Alarms[7]` → `Alarms : Array[0..7] of Bool`), Sort by
    address, Ctrl+Z.
  - Live **layout** by the standard-access rules – each member's offset, the
    size, and whether it matches where the tags are in the PLC (✓ / ⚠).
  - **Import** a UDT sheet (.xlsx/.xls/.csv) or a TYPE / DATA_BLOCK from a
    source (.udt/.scl/.db; a nested UDT is written out as a Struct) and edit it.
  - **Export** as SCL (.udt / .scl – TYPE for Generate blocks from source) or
    as a UDT sheet (.xlsx / .csv), or Copy SCL. The work is kept when the
    window closes.

- **Export → Tag list (.xlsx)** for PLC Monitor: every tag (DB, I, Q, M,
  Modbus, OPC UA) with every field – Name, Address, Area, DB, Offset, Bit,
  Type, Unit, Deadband, Description, Node. Nothing is left out; edit it in
  Excel and bring it back with **Import → Tag list from PLC Monitor
  (.xlsx / .csv)** (or drop it on the window): all fields come back.
- **PLC data types (UDT) from a sheet (.xlsx, .xls, .csv):** one row per
  member – *Name* (nested with dots: `Motor.Speed`), *Data Type* (Bool, Int,
  Real, `Array[0..3] of Int`, Struct, …), optional *Offset* and *Comment*.
  Without offsets the members are laid out by the standard-access rules, as
  for a .udt source. Open or drop the file: the window asks for the **DB
  number** and the **start byte** where the UDT is in the data block, and a
  **tag prefix** (e.g. `Silo1` → `Silo1.Motor.Speed`), and for an **array of
  the UDT** the number of elements and the first index: Motor, 3 from 1 →
  `Motor[1].Run`, `Motor[2].Run`, `Motor[3].Run`, each element right after
  the one before (its size rounded to an even byte).
- **Save as SCL** in the Data blocks window: a UDT sheet becomes its
  `TYPE "Motor_UDT"` and a data block that holds it at the start byte (spare
  bytes before it) once or as `Array[1..3] of "Motor_UDT"` – the addresses
  the window shows. For data blocks, the ticked members as a data block that
  keeps their addresses. For External source files → Generate blocks from
  source in the PLC engineering software.
- **Data blocks window (UDT, SCL sources): members as a tree** – groups by
  name with a box each, Expand all / Collapse all, each member with its
  address worked out from the DB number and start byte. Untick what you
  don't need. Start byte and tag prefix work for SCL data blocks too.
- **Help → Example files:** ready-made spreadsheets (.xlsx) and SCL sources
  for general variables (I, Q, M), a data block (struct + array) and a UDT
  used as an array, each with **Copy** (paste into Excel or the PLC
  engineering software) and **Save** (.xlsx / .csv / .scl). The spreadsheet
  and SCL versions describe the same blocks; every example is tested to
  import as the Help says.
- **Tree view of the tag list** (🌲 Tree view next to the filter): tags
  grouped by their names (`Motor_UDT.Alarms[0]` → Motor_UDT ▸ Alarms ▸ [0]),
  with how many tags each group has and how many are not read. Click a group
  to fold or unfold it, Ctrl/Shift+click to select its tags, **Expand all** /
  **Collapse all**. It turns on by itself after adding a UDT or data block
  from the Data blocks window. The choice and the folded groups are
  remembered.

### Changed

- **Import preview:** every import into the tag list (PLC tag table,
  register map, HMIEditERP, PLC Monitor tag list, CSV, dropped files) first
  shows what it will do – **New** tags, **Changed** ones with old → new
  (address, type, description), **Unchanged** ones, and the rows of the file
  not imported with why. Filter by kind, untick tags, then *Import N tags*
  or Cancel (nothing changes). UDTs and SCL data blocks keep their own
  window (DB number, start byte, members).
- **Insert tag before this…** (right-click a tag, or Shift+Ins) next to
  Insert tag after this…: the new tag goes right above the selected one, its
  address starting after the tag above.
- **The window comes back as it was left** (desktop app): size, position,
  maximized and zoom (kept in `%LOCALAPPDATA%\PLCMonitor\window.json`), and
  the folded button row, Tree view and its folded groups (kept with the
  layout in config.yaml). Before, they were kept in the browser storage,
  which the desktop app loses at every start.
- **New installations start with this look:** theme HMI Gray, the button
  row folded, compact rows, charts in their own window, and a window of
  1000 × 540 at 100 % zoom (config.sample.yaml; tags are not part of it).
  Existing configurations keep their own settings.
- **Spacing** (View → Spacing (buttons and tag list)): **Compact** (small
  buttons like those in the rows, tight rows: the tag list starts about
  90 px higher and shows about twice as many tags), **Normal** (the new
  default) and **Comfortable** (the roomy look of before). It sizes the
  button row, the toolbar above the list (now one line) and the rows.
  Remembered in config.yaml; tag windows use it too.
- **Zoom** without Ctrl+mouse wheel: **− 100 % +** at the right of the menu
  bar, View → Zoom in / Zoom out / Zoom 100 %, Ctrl++ / Ctrl+− / Ctrl+0.
  Every PLC Monitor window gets the same zoom; it is remembered.
- **Fold the button row** (Disconnect, Always on top, Topology, Export,
  Network settings, and the connection error) with ▴ at the right end of the
  menu bar, ▾ to unfold; remembered. While folded, a red dot on the arrow
  shows a connection error (its text on hover).
- **Tools → Connection:** **Connect / Disconnect** (the same as the button –
  there when the button row is folded) and **Connect automatically at
  start** (on by default; unticked, PLC Monitor starts disconnected until
  Connect; kept in config.yaml as `web.no_auto_connect`).
- **Tools menu** in the menu bar (between File and View) instead of File →
  PLC: *PLC* – Scan PLC, PLC tags (OPC UA), UDT tool; *Network* – Topology,
  Network adapters, Windows network connections.
- Desktop app: the name *PLC Monitor* and the connection (pill and address)
  are no longer shown again under the title bar, which shows them already;
  the connection error sits left of the buttons on the same row. The header
  is about half as high. In a web browser they stay.
- No empty band above the tag list when no chart is open (it was the space
  kept for charts): the list starts right under the header.
- **File → Export** is split by who reads the file: *For PLC Monitor – every
  tag* (Tag list .xlsx / .csv), *For TIA Portal – I, Q, M only* (PLC tag
  table .xlsx / .xml / .sdf, Data blocks as SCL source) and *For the HMI*
  (HMIEditERP). The TIA file is saved as `…_TIA.xlsx`; when tags are left
  out the message says where they can go instead.

### Fixed

- Topology (desktop app): the device details panel could not be closed – its
  ✕ was hidden under the window's title bar. The panel now starts below it.

## 1.5.0

### New

- **File → Export → HMI tag list for HMIEditERP (.xls):** the ISO-TCP tags
  (I, Q, M and DB) as the Excel 97-2003 workbook HMIEditERP imports – sheet
  *DSO_DX*, columns Name, Type, Address, ByteOrder, Extend1, Extend2, with
  its address style: `Q0.0`, `MB0_B`, `IW0`, `MD0_D`, and for data blocks
  *DBxDB* + width + DB number (2 digits) + byte (4 digits):
  DB10.DBX1.2 → `DBxDB100001.2`, DB10.DBW1 → `DBxDBW100001`,
  DB10.DBD1 → `DBxDBD100001_D` (Real too), DB3.DBB9 → `DBxDBB030009_B`.
  Type is the width (X, B, W, D); Modbus, OPC UA, LReal tags and DB bytes
  above 9999 are left out.
- **Import of HMIEditERP tag lists (.xls):** File → Import → *PLC tag table /
  register map / HMIEditERP* (or File → Open) reads Excel 97-2003 workbooks
  and HMIEditERP's addresses. Types come back by width: X → Bool, B → Byte,
  W → Word, D → DWord. `DBxDB100001.2` is DB10.DBX1.2; addresses without a
  DB number (`DBxDBW0`) ask for the DB number once.
- **Import says which rows were left out and why:** one row is named in the
  message (*row 9 “Bad1”: bit 9 in DB10.DBX9.9 (0…7)*); more open a list
  with row, name, address and reason (*Copy list* for a report). Notes in
  the file such as *“… exported 2026-11-23”*, titles and empty lines are no
  longer counted as skipped.
- **Drag and drop:** drop a tag table, register map, HMIEditERP list or
  block source on the window to import it (it showed a download before).
  Files dropped on the other windows are no longer opened or downloaded.

### Changed

- **Import → PLC tag table / register map** also reads register maps of HMIs
  and gateways (.xlsx / .csv): a *Key* / *Tag* column for the name, *Type*
  (UINT16, INT32, FLOAT32, … too), *S7Address* with DB addresses
  (`DB10.DBX0.0`, `DB10.DBW78`, `%DB3.DBD4` – tag tables gave only I, Q and
  M before), *Description*, and *Area* + *Address* + *Bit* for Modbus
  (COIL 100, HR 39), used when the network is Modbus. Rows with *Enabled* =
  0 are left out. A *Scale* column is not applied (values are shown raw).

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
